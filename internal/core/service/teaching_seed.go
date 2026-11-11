package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// School days per shift: start, class length, number of classes and after which class each break goes (minutes)
type dayPlan struct {
	start   string
	minutes int
	classes int
	breaks  map[int]int // after class N (1-based) → break minutes
}

var demoDays = map[string]map[string]dayPlan{
	domain.KindEBR: {
		"morning":   {"07:45", 45, 7, map[int]int{3: 20, 5: 10}},
		"afternoon": {"13:00", 45, 7, map[int]int{3: 20, 5: 10}},
	},
	domain.KindEBA: {
		"afternoon": {"14:00", 45, 5, map[int]int{3: 15}},
		"evening":   {"18:30", 45, 5, map[int]int{2: 10}},
	},
	domain.KindEBE:   {"morning": {"08:00", 45, 5, map[int]int{3: 30}}},
	domain.KindCEPRE: {"morning": {"08:00", 45, 6, map[int]int{3: 15}}, "afternoon": {"14:00", 45, 6, map[int]int{3: 15}}, "evening": {"18:00", 45, 5, map[int]int{2: 10}}},
	domain.KindInstitute: {
		"morning": {"08:00", 50, 6, map[int]int{3: 20}},
		"evening": {"18:00", 45, 5, map[int]int{2: 10}},
	},
}

func addMinutes(clock string, minutes int) string {
	h, _ := strconv.Atoi(clock[:2])
	m, _ := strconv.Atoi(clock[3:])
	total := h*60 + m + minutes
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

func (p dayPlan) blocks() []domain.TimeBlock {
	var out []domain.TimeBlock
	at := p.start
	for i := 1; i <= p.classes; i++ {
		end := addMinutes(at, p.minutes)
		out = append(out, domain.TimeBlock{StartsAt: at, EndsAt: end, Kind: "class"})
		at = end
		if b, ok := p.breaks[i]; ok && i < p.classes {
			end = addMinutes(at, b)
			out = append(out, domain.TimeBlock{StartsAt: at, EndsAt: end, Kind: "break"})
			at = end
		}
	}
	for i := range out {
		out[i].Position = i
	}
	return out
}

// Non-teaching staff every demo institution has
var demoOffice = []struct{ position, role string }{
	{"Director(a)", "director"}, {"Coordinador(a) académico(a)", "academic_coordinator"}, {"Secretaria académica", "secretary"},
	{"Psicóloga", "psychologist"}, {"Tesorero", "treasurer"}, {"Auxiliar de educación", "assistant"},
}

// SeedDemoTeaching gives the Joseph demo institutions their school day, staff, teaching load and an automatic
// timetable for the running periods. cmd/seed runs it once
func SeedDemoTeaching(ctx context.Context, academic *AcademicService, teaching *TeachingService, staff port.StaffRepository, slugs []string) (int, error) {
	total := 0
	for n, slug := range slugs {
		inst, err := academic.repo.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
		if _, count, err := staff.StaffList(ctx, domain.StaffFilter{InstitutionID: inst.ID, Limit: 1}); err != nil || count > 0 {
			if err != nil {
				return total, err
			}
			continue
		}
		sc := academicScope{institutionScope: institutionScope{institution: inst, role: domain.RoleSuperadmin}, profile: profileFor(inst.Kind)}
		placed, err := seedTeaching(ctx, sc, n, academic, teaching, staff)
		total += placed
		if err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
	}
	return total, nil
}

func seedTeaching(ctx context.Context, sc academicScope, n int, academic *AcademicService, teaching *TeachingService, staff port.StaffRepository) (int, error) {
	inst := sc.institution
	rnd := rand.New(rand.NewPCG(uint64(n+10), 4))
	settings, err := academic.settings(ctx, sc)
	if err != nil {
		return 0, err
	}
	if sc.profile.weekDays != nil && len(settings.WeekDays) != len(sc.profile.weekDays) {
		settings.WeekDays = sc.profile.weekDays
		if _, err := academic.repo.SaveSettings(ctx, inst.ID, settings); err != nil {
			return 0, err
		}
	}
	for shift, plan := range demoDays[inst.Kind] {
		if err := teaching.repo.ReplaceTimeBlocks(ctx, inst.ID, shift, plan.blocks()); err != nil {
			return 0, err
		}
	}

	dni := 30_000_000 + n*1_000_000
	person := func(position, specialty string, teacher bool, hours int) (domain.Staff, error) {
		dni += 1 + rnd.IntN(9)
		female := rnd.IntN(2) == 0
		given := adultMale[rnd.IntN(len(adultMale))]
		if female {
			given = adultFemale[rnd.IntN(len(adultFemale))]
		}
		surnames := demoSurnames[rnd.IntN(len(demoSurnames))] + " " + demoSurnames[rnd.IntN(len(demoSurnames))]
		in := domain.StaffInput{DocumentType: "dni", DocumentNumber: strconv.Itoa(dni), GivenNames: given, Surnames: surnames, Position: position, IsTeacher: teacher,
			Specialty: specialty, Status: "active", Phone: fmt.Sprintf("9%02d %03d %03d", rnd.IntN(100), rnd.IntN(1000), rnd.IntN(1000)),
			Email: fmt.Sprintf("%s.%s%d@personal.test", ascii(given), ascii(strings.Fields(surnames)[0]), rnd.IntN(90)+10)}
		if teacher {
			in.MaxWeeklyHours = &hours
		}
		return staff.CreateStaff(ctx, inst.ID, in)
	}
	for _, o := range demoOffice {
		if o.role == "assistant" && inst.Kind != domain.KindEBR && inst.Kind != domain.KindEBE {
			continue
		}
		if _, err := person(o.position, "", false, 0); err != nil {
			return 0, err
		}
	}

	periods, err := academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	levels, err := academic.repo.Levels(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	levelCode := map[string]string{}
	for _, l := range levels {
		levelCode[l.ID] = l.Code
	}
	// Teachers by pool (a subject code, a classroom, a program): reused until their hours are full
	type load struct {
		staff domain.Staff
		hours int
	}
	pools := map[string][]*load{}
	capacity := 28
	pick := func(pool, specialty string, hours int) (string, error) {
		for _, l := range pools[pool] {
			if l.hours+hours <= capacity {
				l.hours += hours
				return l.staff.ID, nil
			}
		}
		st, err := person("Docente", specialty, true, 30)
		if err != nil {
			return "", err
		}
		pools[pool] = append(pools[pool], &load{staff: st, hours: hours})
		return st.ID, nil
	}
	placed := 0
	for _, p := range periods {
		if p.Status != domain.PeriodActive {
			continue
		}
		items, err := teaching.loadFromPlans(ctx, sc, p.ID)
		if err != nil {
			return placed, err
		}
		if _, err := teaching.repo.CreateAssignments(ctx, inst.ID, items); err != nil {
			return placed, err
		}
		assignments, err := teaching.repo.Assignments(ctx, inst.ID, p.ID)
		if err != nil {
			return placed, err
		}
		sections, err := academic.repo.Sections(ctx, inst.ID, p.ID)
		if err != nil {
			return placed, err
		}
		sectionByID := map[string]domain.Section{}
		for _, s := range sections {
			sectionByID[s.ID] = s
		}
		tutor := map[string]string{}
		for _, a := range assignments {
			sec := sectionByID[a.SectionID]
			code := levelCode[sec.LevelID]
			var pool string
			switch {
			// One classroom teacher per section: inicial, EBE, EBA; in primaria all but the specialists
			case inst.Kind == domain.KindEBE || inst.Kind == domain.KindEBA || code == "inicial" ||
				(code == "primaria" && a.SubjectCode != "educacion-fisica" && a.SubjectCode != "ingles" && a.SubjectCode != "educacion-religiosa"):
				pool = "section:" + sec.ID
				capacity = 40
			case inst.Kind == domain.KindInstitute && strings.Contains(a.SubjectKind, "unit"):
				pool = "program:" + sec.LevelID + ":" + a.SubjectCode
				capacity = 28
				if isEmployability(a.SubjectCode) {
					pool = "employability:" + a.SubjectCode
				}
			default:
				pool = "subject:" + a.SubjectCode
				capacity = 28
			}
			specialty := a.SubjectName
			if strings.HasPrefix(pool, "section:") {
				specialty = "Docente de aula · " + sec.LevelName
			}
			id, err := pick(pool, specialty, a.WeeklyHours)
			if err != nil {
				return placed, err
			}
			if _, err := teaching.repo.UpdateAssignment(ctx, inst.ID, a.ID, id, a.WeeklyHours); err != nil {
				return placed, err
			}
			if _, ok := tutor[sec.ID]; !ok || strings.HasPrefix(pool, "section:") {
				tutor[sec.ID] = id
			}
		}
		for sectionID, staffID := range tutor {
			if err := teaching.repo.SetTutor(ctx, inst.ID, sectionID, staffID); err != nil {
				return placed, err
			}
		}
		res, err := teaching.autoFill(ctx, sc, p.ID, "")
		if err != nil {
			return placed, err
		}
		placed += res.Placed
	}
	return placed, nil
}

func isEmployability(code string) bool {
	for _, c := range []string{"comunicacion-oral", "aplicaciones-en-internet", "comprension-y-redaccion-de-textos", "ingles-para-la-comunicacion-oral", "solucion-de-problemas", "etica-y-deontologia", "oportunidades-de-negocio", "ingles-tecnico", "plan-de-negocios", "innovacion-tecnologica", "trabajo-colaborativo", "investigacion-aplicada"} {
		if code == c {
			return true
		}
	}
	return false
}
