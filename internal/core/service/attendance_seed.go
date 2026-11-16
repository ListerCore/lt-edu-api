package service

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strconv"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Demo attendance covers the last weeks of the running periods; classes are taken for the last days only
const (
	demoAttendanceDays = 30
	demoClassDays      = 8
)

// attendanceHabit is how a demo student attends: most come every day, a few miss often (they feed the alerts)
type attendanceHabit struct {
	absent, late float64
	// Institutes: misses some units more than others (the 30 % rule)
	skipsUnits bool
}

func habitFor(studentID string) attendanceHabit {
	h := fnv.New32a()
	_, _ = h.Write([]byte(studentID))
	switch v := h.Sum32() % 100; {
	case v < 4:
		return attendanceHabit{absent: 0.24, late: 0.12, skipsUnits: true}
	case v < 12:
		return attendanceHabit{absent: 0.09, late: 0.10}
	default:
		return attendanceHabit{absent: 0.02, late: 0.04}
	}
}

func localAt(day time.Time, clock string, plus int) time.Time {
	hh, _ := strconv.Atoi(clock[:2])
	mm, _ := strconv.Atoi(clock[3:])
	return time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, lima()).Add(time.Duration(plus) * time.Minute)
}

// SeedDemoAttendance fills the ListerCore Edu demo institutions with the attendance of the last weeks: gate arrivals, a few
// absences (some justified, some waiting for review), the staff days and the classes of the last days. cmd/seed
// runs it once
func SeedDemoAttendance(ctx context.Context, svc *AttendanceService, seeder port.AttendanceSeeder, slugs []string) (int, error) {
	total := 0
	for n, slug := range slugs {
		inst, err := svc.academic.repo.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
		if done, err := seeder.HasAttendance(ctx, inst.ID); err != nil || done {
			if err != nil {
				return total, err
			}
			continue
		}
		sc := attendanceScope{academicScope: academicScope{institutionScope: institutionScope{institution: inst, role: domain.RoleSuperadmin}, profile: profileFor(inst.Kind)}}
		marks, err := seedAttendance(ctx, svc, seeder, sc, n)
		total += marks
		if err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
	}
	return total, nil
}

func seedAttendance(ctx context.Context, svc *AttendanceService, seeder port.AttendanceSeeder, sc attendanceScope, n int) (int, error) {
	inst := sc.institution
	rnd := rand.New(rand.NewPCG(uint64(n+20), 5))
	settings, err := svc.settings(ctx, sc)
	if err != nil {
		return 0, err
	}
	blocks, err := svc.teaching.TimeBlocks(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	periods, err := svc.academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	today := svc.today()
	var days []time.Time
	for d := today.AddDate(0, 0, -1); len(days) < demoAttendanceDays && d.After(today.AddDate(0, 0, -90)); d = d.AddDate(0, 0, -1) {
		if schoolDay(settings, d) {
			days = append([]time.Time{d}, days...)
		}
	}
	firstClass := func(shift string) string {
		first := ""
		for _, b := range blocks {
			if b.Kind == "class" && b.Shift == shift && (first == "" || b.StartsAt < first) {
				first = b.StartsAt
			}
		}
		return first
	}

	type absence struct {
		student port.SeedStudent
		day     time.Time
	}
	var marks []domain.DailyMark
	var absences []absence
	status := map[string]string{} // student|day → status, for the classes
	for _, p := range periods {
		if p.Status != domain.PeriodActive {
			continue
		}
		roster, err := seeder.SeedRoster(ctx, inst.ID, p.ID)
		if err != nil {
			return 0, err
		}
		for _, st := range roster {
			habit := habitFor(st.StudentID)
			start := firstClass(st.Shift)
			for _, d := range days {
				if d.Before(p.StartsOn) || d.After(p.EndsOn) {
					continue
				}
				m := domain.DailyMark{PersonID: st.StudentID, Day: d, Source: domain.SourceGate}
				switch r := rnd.Float64(); {
				case r < habit.absent:
					m.Status, m.Source = domain.MarkAbsent, domain.SourceClosing
					absences = append(absences, absence{st, d})
				case r < habit.absent+habit.late && start != "":
					m.Status = domain.MarkLate
					t := localAt(d, start, settings.LateAfterMinutes+1+rnd.IntN(25))
					m.ArrivedAt = &t
				default:
					m.Status = domain.MarkPresent
					if start != "" {
						t := localAt(d, start, -rnd.IntN(25))
						m.ArrivedAt = &t
					} else {
						m.Source = domain.SourceManual
					}
				}
				marks = append(marks, m)
				status[st.StudentID+"|"+formatDay(d)] = m.Status
			}
		}
	}
	stored, err := seeder.BulkDailyMarks(ctx, inst.ID, marks)
	if err != nil {
		return 0, err
	}

	// A third of the absences were justified by the family and approved; a few recent ones wait for review
	reasons := []struct{ reason, text string }{
		{"health", "Tuvo fiebre y lo llevamos al centro de salud. Adjuntaremos la constancia médica en secretaría."},
		{"health", "Amaneció con malestar estomacal y se quedó en casa en reposo."},
		{"family", "Viajamos por un asunto familiar urgente."},
		{"procedure", "Tenía cita para tramitar su DNI en RENIEC."},
		{"other", "Hubo un corte de transporte en nuestra zona y no pudo llegar."},
	}
	pending := 0
	for i, a := range absences {
		recent := !a.day.Before(today.AddDate(0, 0, -7))
		approve := rnd.IntN(3) == 0 && !recent
		ask := recent && pending < 6 && rnd.IntN(2) == 0
		if !approve && !ask {
			continue
		}
		r := reasons[i%len(reasons)]
		name := a.student.GuardianName
		if name == "" {
			name = "Apoderado(a)"
		}
		j, err := svc.repo.CreateJustification(ctx, inst.ID, domain.Justification{StudentID: a.student.StudentID, FromDay: a.day, ToDay: a.day, Reason: r.reason, Description: r.text, SubmittedName: name}, "")
		if err != nil {
			return stored, err
		}
		if approve {
			if _, err := svc.repo.ReviewJustification(ctx, inst.ID, j.ID, domain.JustificationApproved, "", "", "Secretaría académica"); err != nil {
				return stored, err
			}
			status[a.student.StudentID+"|"+formatDay(a.day)] = domain.MarkJustified
		} else {
			pending++
		}
	}

	// The staff come almost every day
	staffRows, err := svc.repo.StaffDay(ctx, inst.ID, today)
	if err != nil {
		return stored, err
	}
	staffStart := lateAfter(blocks, "", 0)
	var staffMarks []domain.DailyMark
	for _, s := range staffRows {
		for _, d := range days {
			m := domain.DailyMark{PersonID: s.StaffID, Day: d, Status: domain.MarkPresent, Source: domain.SourceGate}
			switch r := rnd.Float64(); {
			case r < 0.015:
				m.Status, m.Source = domain.MarkAbsent, domain.SourceClosing
			case r < 0.06 && staffStart != "":
				m.Status = domain.MarkLate
				t := localAt(d, staffStart, settings.LateAfterMinutes+1+rnd.IntN(15))
				m.ArrivedAt = &t
			case staffStart != "":
				t := localAt(d, staffStart, -5-rnd.IntN(25))
				m.ArrivedAt = &t
			default:
				m.Source = domain.SourceManual
			}
			staffMarks = append(staffMarks, m)
		}
	}
	if _, err := seeder.BulkStaffMarks(ctx, inst.ID, staffMarks); err != nil {
		return stored, err
	}

	// Classes of the last days, following each student's day; institutes add the units some students skip
	var running []string
	for _, p := range periods {
		if p.Status == domain.PeriodActive {
			running = append(running, p.ID)
		}
	}
	if len(running) == 0 {
		return stored, nil
	}
	entries, err := svc.teaching.Entries(ctx, inst.ID, running)
	if err != nil {
		return stored, err
	}
	classDays := days
	if len(classDays) > demoClassDays {
		classDays = classDays[len(classDays)-demoClassDays:]
	}
	assignments := map[string]domain.Assignment{}
	for _, d := range classDays {
		wd := isoWeekday(d)
		first := map[string]string{} // section → first class block of the day
		for _, e := range entries {
			if e.Weekday == wd && (first[e.SectionID] == "" || e.StartsAt < first[e.SectionID]) {
				first[e.SectionID] = e.StartsAt
			}
		}
		for _, e := range entries {
			if e.Weekday != wd {
				continue
			}
			a, ok := assignments[e.AssignmentID]
			if !ok {
				if a, err = svc.teaching.Assignment(ctx, inst.ID, e.AssignmentID); err != nil {
					return stored, err
				}
				assignments[e.AssignmentID] = a
			}
			roster, err := svc.repo.ClassRoster(ctx, inst.ID, e.SectionID, a.SubjectID, "", d)
			if err != nil {
				return stored, err
			}
			out := make([]domain.ClassMark, 0, len(roster))
			for _, row := range roster {
				st := status[row.StudentID+"|"+formatDay(d)]
				switch {
				case st == domain.MarkAbsent || st == domain.MarkJustified:
				case st == domain.MarkLate && e.StartsAt == first[e.SectionID]:
				case inst.Kind == domain.KindInstitute && habitFor(row.StudentID).skipsUnits && (hashOf(row.StudentID+a.SubjectID)%3 == 0) && rnd.Float64() < 0.55:
					st = domain.MarkAbsent
				default:
					st = domain.MarkPresent
				}
				if st == "" {
					st = domain.MarkPresent
				}
				out = append(out, domain.ClassMark{StudentID: row.StudentID, Status: st})
			}
			if len(out) == 0 {
				continue
			}
			topic := fmt.Sprintf("Sesión del %s", d.Format("02/01"))
			if _, err := svc.repo.SaveSession(ctx, inst.ID, domain.ClassSession{AssignmentID: e.AssignmentID, SectionID: e.SectionID, Day: d, BlockID: e.BlockID, Topic: topic}, out); err != nil {
				return stored, err
			}
		}
	}
	return stored, nil
}

func formatDay(d time.Time) string { return d.Format(time.DateOnly) }

func hashOf(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
