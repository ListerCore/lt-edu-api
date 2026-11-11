package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"

	"api/internal/core/domain"
)

// ProgramsSeeder writes what the institute demo needs in bulk (cmd/seed only)
type ProgramsSeeder interface {
	HasPrograms(ctx context.Context, institutionID string) (bool, error)
	SeedUnitRecords(ctx context.Context, institutionID string, rows [][5]any) error
	SeedGraduates(ctx context.Context, institutionID, levelID, gradeID, periodID string, people [][3]string) ([]string, error)
	LastGrade(ctx context.Context, levelID string) (string, error)
	SeedLogs(ctx context.Context, internshipID string, logs []domain.InternshipLog) error
}

var demoCompanies = map[string][]struct{ name, area, supervisor string }{
	"enfermeria-tecnica": {
		{"Clínica Santa Rosa (demo)", "Hospitalización", "Lic. Teresa Ccori Ramos"},
		{"Centro de Salud Belén (demo)", "Atención primaria", "Lic. Julio Mendoza Pari"},
		{"Hospital Regional del Sur (demo)", "Emergencia", "Lic. Carmen Quispe Soto"},
	},
	"desarrollo-sistemas": {
		{"Andes Software SAC (demo)", "Desarrollo web", "Ing. Raúl Huamán Torres"},
		{"Municipalidad Distrital de Carmen Alto (demo)", "Soporte técnico", "Ing. Silvia Rojas Vilca"},
		{"Cooperativa Santa María (demo)", "Sistemas", "Ing. Óscar Paredes Medina"},
	},
	"contabilidad": {
		{"Estudio Contable Ayala & Asociados (demo)", "Contabilidad", "CPC Rosa Ayala Chávez"},
		{"Distribuidora del Centro EIRL (demo)", "Tesorería", "CPC Víctor Flores Ríos"},
		{"Caja Municipal Wari (demo)", "Créditos", "CPC Norma Salazar Tito"},
	},
}

var demoActivities = []string{"Apoyo en la atención de usuarios", "Registro y control de información", "Elaboración de informes", "Apoyo en procedimientos del área", "Revisión de documentos con el supervisor"}

// semesterName goes back from the running semester: steps 1 is the previous one
func semesterName(year, half, steps int) string {
	for range steps {
		if half == 2 {
			half = 1
		} else {
			half, year = 2, year-1
		}
	}
	return fmt.Sprintf("Semestre %d-%s", year, map[int]string{1: "I", 2: "II"}[half])
}

// SeedDemoPrograms gives the institute demo its students' history: past semesters passed, work placements
// approved or under way with their hours, modular certificates, three graduates of Enfermería Técnica (one with
// the degree) and a program coordinator. cmd/seed runs it once
func SeedDemoPrograms(ctx context.Context, svc *ProgramsService, seeder ProgramsSeeder, slug string) (int, error) {
	inst, err := svc.assessment.academic.repo.InstitutionBySlug(ctx, slug)
	if errors.Is(err, domain.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if done, err := seeder.HasPrograms(ctx, inst.ID); err != nil || done {
		return 0, err
	}
	sc := programScope{attendanceScope: attendanceScope{academicScope: academicScope{institutionScope: institutionScope{institution: inst, role: domain.RoleSuperadmin}, profile: profileFor(inst.Kind)}}, passing: 13}
	sc.userName = "Secretaría académica"
	rnd := rand.New(rand.NewPCG(99, 9))
	today := svc.assessment.attendance.today()
	programs, err := svc.repo.Programs(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	periods, err := svc.assessment.academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	var closed domain.Period
	for _, p := range periods {
		if p.Status == domain.PeriodClosed {
			closed = p
		}
	}
	issued := 0
	for pi, prog := range programs {
		_, units, err := svc.repo.Structure(ctx, inst.ID, prog.LevelID)
		if err != nil {
			return issued, err
		}
		var graduates []string
		if pi == 0 && closed.ID != "" {
			grade, err := seeder.LastGrade(ctx, prog.LevelID)
			if err != nil {
				return issued, err
			}
			people := [][3]string{{"Rosa Elena", "Huamán", "Quispe"}, {"Julio César", "Ccori", "Mendoza"}, {"Gloria", "Paredes", "Flores"}}
			if graduates, err = seeder.SeedGraduates(ctx, inst.ID, prog.LevelID, grade, closed.ID, people); err != nil {
				return issued, err
			}
		}
		students, err := svc.repo.Students(ctx, inst.ID, prog.LevelID, "")
		if err != nil {
			return issued, err
		}
		companies := demoCompanies[prog.Code]
		var records [][5]any
		type place struct {
			student string
			unit    domain.UnitRef
			done    bool
			share   float64
		}
		var places []place
		for _, st := range students {
			current := st.SemesterPos
			graduate := slices.Contains(graduates, st.ID)
			if graduate {
				current = 99
			}
			a := ability(st.ID)
			for _, u := range units {
				if u.SemesterPos >= current {
					if u.Kind == "efsrt" && u.SemesterPos == current {
						// The placement of the running semester: under way
						places = append(places, place{st.ID, u, false, 0.35 + rnd.Float64()*0.75})
					}
					continue
				}
				if u.Kind == "efsrt" {
					places = append(places, place{st.ID, u, true, 1})
					continue
				}
				grade := 11 + int(a*7) + rnd.IntN(3)
				status := "approved"
				if !graduate && rnd.IntN(40) == 0 {
					grade, status = 8+rnd.IntN(4), "failed"
				}
				steps := current - u.SemesterPos
				if graduate {
					steps = 7 - u.SemesterPos
				}
				records = append(records, [5]any{st.ID, u.SubjectID, semesterName(today.Year(), 2, steps), min(20, grade), status})
			}
		}
		if len(records) > 0 {
			if err := seeder.SeedUnitRecords(ctx, inst.ID, records); err != nil {
				return issued, err
			}
		}
		for _, p := range places {
			c := companies[rnd.IntN(len(companies))]
			hours := requiredHours(p.unit)
			start := today.AddDate(0, -4, -rnd.IntN(20))
			if p.done {
				start = today.AddDate(0, -6*(1+rnd.IntN(2)), 0)
			}
			x, err := svc.repo.SaveInternship(ctx, inst.ID, domain.Internship{StudentID: p.student, SubjectID: p.unit.SubjectID, Company: c.name, CompanyRUC: fmt.Sprintf("20%09d", 100000000+rnd.IntN(800000000)),
				Area: c.area, Supervisor: c.supervisor, SupervisorContact: fmt.Sprintf("9%02d %03d %03d", rnd.IntN(100), rnd.IntN(1000), rnd.IntN(1000)), StartsOn: start, RequiredHours: hours})
			if err != nil {
				return issued, err
			}
			target := int(float64(hours) * p.share)
			if p.done {
				target = hours
			}
			if target > hours && rnd.IntN(2) == 0 {
				target = hours
			}
			var logs []domain.InternshipLog
			day := start
			for total := 0; total < target; {
				h := min(6, target-total)
				logs = append(logs, domain.InternshipLog{Day: day, Hours: float64(h), Activity: demoActivities[rnd.IntN(len(demoActivities))]})
				total += h
				day = day.AddDate(0, 0, 1+rnd.IntN(2))
				if day.After(today) {
					day = today
				}
			}
			if err := seeder.SeedLogs(ctx, x.ID, logs); err != nil {
				return issued, err
			}
			if p.done {
				score := 14 + rnd.IntN(6)
				if err := svc.repo.Evaluate(ctx, inst.ID, x.ID, "approved", score, "Cumplió las tareas asignadas con responsabilidad y buen trato a los usuarios.", c.supervisor, start.AddDate(0, 2, 0)); err != nil {
					return issued, err
				}
			}
		}
		// Modular certificates: most finished modules already have theirs; the rest wait for secretaría
		for _, st := range students {
			rec, err := svc.record(ctx, sc, st.ID)
			if err != nil {
				return issued, err
			}
			for _, m := range rec.Modules {
				if m.Complete && m.Certificate == nil && (slices.Contains(graduates, st.ID) || rnd.IntN(10) < 7) {
					if _, err := svc.issueModular(ctx, sc, rec, m.Module.ID); err != nil {
						return issued, err
					}
					issued++
				}
			}
		}
		// Graduates: the first has everything and the degree, the second lacks the language, the third is ready
		for gi, id := range graduates {
			for _, m := range manualRequirements {
				done := !(gi == 1 && m.Code == "language")
				note := ""
				if m.Code == "project" && done {
					note = "Trabajo de aplicación profesional sustentado y aprobado."
				}
				if err := svc.repo.SetRequirement(ctx, id, domain.Requirement{Code: m.Code, Done: done, Note: note, CheckedName: "Secretaría académica"}); err != nil {
					return issued, err
				}
			}
			if gi == 0 {
				rec, err := svc.record(ctx, sc, id)
				if err != nil {
					return issued, err
				}
				if _, err := svc.issueDegree(ctx, sc, rec); err != nil {
					return issued, fmt.Errorf("degree: %w", err)
				}
				issued++
			}
		}
	}
	// A program coordinator for Enfermería Técnica
	if len(programs) > 0 {
		staff, _, err := svc.assessment.attendance.staff.StaffList(ctx, domain.StaffFilter{InstitutionID: inst.ID, Status: "active", TeachersOnly: true, Limit: 1})
		if err == nil && len(staff) > 0 {
			if err := svc.repo.SetCoordinator(ctx, inst.ID, programs[0].LevelID, staff[0].ID); err != nil {
				return issued, err
			}
		}
	}
	return issued, nil
}
