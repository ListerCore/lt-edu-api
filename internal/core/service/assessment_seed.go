package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ability is a stable 0–1 for each demo student, so their levels look like the same child across areas and terms
func ability(studentID string) float64 {
	return float64(hashOf("ability:"+studentID)%1000) / 1000
}

func levelFor(a float64, rnd *rand.Rand, term int) string {
	v := a*0.75 + rnd.Float64()*0.35 + float64(term)*0.015
	switch {
	case v > 0.86:
		return "AD"
	case v > 0.42:
		return "A"
	case v > 0.2:
		return "B"
	default:
		return "C"
	}
}

var (
	conclusionsC = []string{
		"Aún muestra dificultad en «%s». Se le acompañará con actividades guiadas en clase y una ficha semanal para trabajar en casa.",
		"Está en inicio en la competencia «%s». Necesita apoyo cercano: se reforzará en pequeños grupos y se coordinará con la familia.",
	}
	conclusionsB = []string{
		"Avanza en «%s», pero todavía necesita apoyo para hacerlo de forma autónoma.",
		"Va logrando la competencia «%s»; requiere más práctica para consolidarla.",
	}
	tutorComments = []string{
		"Muestra interés y buena disposición en clase. Le animamos a seguir organizando su tiempo de estudio en casa.",
		"Participa con entusiasmo y se relaciona bien con sus compañeros. Debe cuidar la puntualidad en la entrega de tareas.",
		"Ha mejorado su concentración durante el periodo. Recomendamos leer en familia al menos veinte minutos al día.",
		"Es respetuoso(a) y colaborador(a). Necesita reforzar la revisión de sus trabajos antes de entregarlos.",
	}
)

// SeedDemoAssessment grades the Joseph demo institutions: every closed term complete (levels, conclusions, conduct,
// tutor comments and the teachers' "sent"), part of the running term, and evaluations 0–20 in IEST and CEPRE.
// cmd/seed runs it once
func SeedDemoAssessment(ctx context.Context, svc *AssessmentService, seeder port.AssessmentSeeder, slugs []string) (int, error) {
	total := 0
	for n, slug := range slugs {
		inst, err := svc.academic.repo.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return total, err
		}
		if done, err := seeder.HasAssessment(ctx, inst.ID); err != nil || done {
			if err != nil {
				return total, err
			}
			continue
		}
		asc := attendanceScope{academicScope: academicScope{institutionScope: institutionScope{institution: inst, role: domain.RoleSuperadmin}, profile: profileFor(inst.Kind)}}
		settings, err := svc.academic.settings(ctx, asc.academicScope)
		if err != nil {
			return total, err
		}
		sc := gradeScope{attendanceScope: asc, settings: settings}
		rnd := rand.New(rand.NewPCG(uint64(n+30), 6))
		var count int
		if sc.numeric() {
			count, err = seedScores(ctx, svc, seeder, sc, rnd)
		} else {
			count, err = seedLevels(ctx, svc, seeder, sc, rnd)
		}
		total += count
		if err != nil {
			return total, fmt.Errorf("%s: %w", slug, err)
		}
	}
	return total, nil
}

func seedLevels(ctx context.Context, svc *AssessmentService, seeder port.AssessmentSeeder, sc gradeScope, rnd *rand.Rand) (int, error) {
	inst := sc.institution
	periods, err := svc.academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	today := svc.attendance.today()
	total := 0
	for _, pr := range periods {
		if pr.Status != domain.PeriodActive {
			continue
		}
		p, err := svc.academic.repo.Period(ctx, inst.ID, pr.ID)
		if err != nil {
			return total, err
		}
		sections, err := svc.academic.repo.Sections(ctx, inst.ID, p.ID)
		if err != nil {
			return total, err
		}
		var marks []port.SeedMark
		var conduct []port.SeedConduct
		var comments []port.SeedComment
		type sent struct{ section, assignment, term, name string }
		var sends []sent
		for _, sec := range sections {
			students, err := svc.repo.Students(ctx, inst.ID, sec.ID, "")
			if err != nil {
				return total, err
			}
			if len(students) == 0 {
				continue
			}
			subjects, err := svc.repo.SubjectsOfSection(ctx, inst.ID, sec.ID)
			if err != nil {
				return total, err
			}
			trans, err := svc.repo.Transversal(ctx, inst.ID, sec.ID)
			if err != nil {
				return total, err
			}
			meta, err := svc.repo.Section(ctx, inst.ID, sec.ID)
			if err != nil {
				return total, err
			}
			type sheet struct {
				assignment, staff string
				comps             []domain.CompetencyRef
			}
			var sheets []sheet
			for _, a := range subjects {
				comps, err := svc.repo.Competencies(ctx, inst.ID, a.SubjectID)
				if err != nil {
					return total, err
				}
				if len(comps) > 0 {
					sheets = append(sheets, sheet{a.ID, a.StaffName, comps})
				}
			}
			if len(trans) > 0 {
				sheets = append(sheets, sheet{"", meta.TutorName, trans})
			}
			for ti, t := range p.Terms {
				closed := t.Status == domain.TermClosed
				if !closed && t.StartsOn.After(today) {
					continue
				}
				for _, sh := range sheets {
					// The running term: about half of the sheets are already filled, a third of those sent
					if !closed && rnd.Float64() > 0.5 {
						continue
					}
					for _, st := range students {
						a := ability(st.ID)
						for _, c := range sh.comps {
							lvl := levelFor(a, rnd, ti)
							m := port.SeedMark{SectionID: sec.ID, TermID: t.ID, CompetencyMark: domain.CompetencyMark{StudentID: st.ID, CompetencyID: c.ID, Level: lvl}}
							switch {
							case lvl == "C":
								m.Conclusion = fmt.Sprintf(conclusionsC[rnd.IntN(len(conclusionsC))], c.Name)
							case lvl == "B" && rnd.IntN(4) == 0:
								m.Conclusion = fmt.Sprintf(conclusionsB[rnd.IntN(len(conclusionsB))], c.Name)
							}
							marks = append(marks, m)
						}
					}
					if closed || rnd.IntN(3) == 0 {
						name := sh.staff
						if name == "" {
							name = "Docente"
						}
						sends = append(sends, sent{sec.ID, sh.assignment, t.ID, name})
					}
				}
				if !closed {
					continue
				}
				for _, st := range students {
					if sc.settings.ConductGrade {
						score := 13 + int(ability(st.ID)*6) + rnd.IntN(3) - 1
						conduct = append(conduct, port.SeedConduct{SectionID: sec.ID, TermID: t.ID, StudentID: st.ID, Score: min(20, max(11, score))})
					}
					comments = append(comments, port.SeedComment{SectionID: sec.ID, TermID: t.ID, StudentID: st.ID, Comment: tutorComments[rnd.IntN(len(tutorComments))]})
				}
			}
		}
		n, err := seeder.BulkCompetencyMarks(ctx, inst.ID, marks)
		total += n
		if err != nil {
			return total, err
		}
		if len(conduct) > 0 {
			if err := seeder.BulkConduct(ctx, inst.ID, conduct); err != nil {
				return total, err
			}
		}
		if err := seeder.BulkComments(ctx, comments); err != nil {
			return total, err
		}
		for _, x := range sends {
			if err := svc.repo.SetSubmission(ctx, inst.ID, x.section, x.assignment, x.term, true, "", x.name); err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

// IEST units: four indicators of achievement (the last one, the final evaluation, still ahead); CEPRE courses:
// weekly practices
func seedScores(ctx context.Context, svc *AssessmentService, seeder port.AssessmentSeeder, sc gradeScope, rnd *rand.Rand) (int, error) {
	inst := sc.institution
	periods, err := svc.academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	today := svc.attendance.today()
	total := 0
	for _, p := range periods {
		if p.Status != domain.PeriodActive || p.StartsOn.After(today) {
			continue
		}
		assignments, err := svc.teaching.Assignments(ctx, inst.ID, p.ID)
		if err != nil {
			return total, err
		}
		// How far the period went: evaluations whose date already passed get scores
		span := p.EndsOn.Sub(p.StartsOn)
		var plan []struct {
			name   string
			weight int
			at     time.Time
		}
		if inst.Kind == domain.KindInstitute {
			for i, name := range []string{"Indicador de logro 1", "Indicador de logro 2", "Indicador de logro 3", "Evaluación final"} {
				plan = append(plan, struct {
					name   string
					weight int
					at     time.Time
				}{name, 25, p.StartsOn.Add(span * time.Duration(i+1) / 4).Add(-72 * time.Hour)})
			}
		} else {
			for d, i := p.StartsOn.AddDate(0, 0, 5), 1; !d.After(p.EndsOn); d, i = d.AddDate(0, 0, 7), i+1 {
				plan = append(plan, struct {
					name   string
					weight int
					at     time.Time
				}{fmt.Sprintf("Práctica calificada %d", i), 1, d})
			}
		}
		var scores []domain.Score
		for _, a := range assignments {
			if a.SubjectKind == "efsrt" {
				continue
			}
			students, err := svc.repo.Students(ctx, inst.ID, a.SectionID, a.SubjectID)
			if err != nil {
				return total, err
			}
			if len(students) == 0 {
				continue
			}
			for _, ev := range plan {
				// Only the evaluations already given and the next one are in the register
				if ev.at.After(today.AddDate(0, 0, 14)) {
					continue
				}
				held := ev.at
				e, err := svc.repo.SaveEvaluation(ctx, inst.ID, domain.Evaluation{AssignmentID: a.ID, Name: ev.name, Weight: ev.weight, HeldOn: &held})
				if err != nil {
					return total, err
				}
				if ev.at.After(today) {
					continue
				}
				for _, st := range students {
					// A few missed the evaluation and have no score yet
					if rnd.IntN(25) == 0 {
						continue
					}
					v := 9 + int(ability(st.ID)*10) + rnd.IntN(5) - 2
					scores = append(scores, domain.Score{EvaluationID: e.ID, StudentID: st.ID, Score: min(20, max(0, v))})
				}
			}
		}
		if len(scores) > 0 {
			if err := seeder.BulkScores(ctx, scores); err != nil {
				return total, err
			}
		}
		total += len(scores)
	}
	return total, nil
}

// SeedDemoClosing completes the running term of the last grade of primaria and secundaria in the EBR demo, so the
// year closing has whole sections to preview (the term stays open: closing it is part of the demo)
func SeedDemoClosing(ctx context.Context, svc *AssessmentService, seeder port.AssessmentSeeder, slug string) (int, error) {
	inst, err := svc.academic.repo.InstitutionBySlug(ctx, slug)
	if errors.Is(err, domain.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	periods, err := svc.academic.repo.Periods(ctx, inst.ID)
	if err != nil {
		return 0, err
	}
	rnd := rand.New(rand.NewPCG(77, 7))
	total := 0
	for _, pr := range periods {
		if pr.Status != domain.PeriodActive {
			continue
		}
		p, err := svc.academic.repo.Period(ctx, inst.ID, pr.ID)
		if err != nil || len(p.Terms) == 0 {
			return total, err
		}
		last := p.Terms[len(p.Terms)-1]
		sections, err := svc.academic.repo.Sections(ctx, inst.ID, p.ID)
		if err != nil {
			return total, err
		}
		for _, sec := range sections {
			meta, err := svc.repo.Section(ctx, inst.ID, sec.ID)
			if err != nil {
				return total, err
			}
			if !(meta.LevelName == "Primaria" && strings.HasPrefix(meta.Label, "6.°") || meta.LevelName == "Secundaria" && strings.HasPrefix(meta.Label, "5.°")) {
				continue
			}
			students, err := svc.repo.Students(ctx, inst.ID, sec.ID, "")
			if err != nil {
				return total, err
			}
			subjects, err := svc.repo.SubjectsOfSection(ctx, inst.ID, sec.ID)
			if err != nil {
				return total, err
			}
			trans, err := svc.repo.Transversal(ctx, inst.ID, sec.ID)
			if err != nil {
				return total, err
			}
			comps := trans
			for _, a := range subjects {
				c, err := svc.repo.Competencies(ctx, inst.ID, a.SubjectID)
				if err != nil {
					return total, err
				}
				comps = append(comps, c...)
			}
			existing, err := svc.repo.CompetencyMarks(ctx, inst.ID, sec.ID, last.ID, false)
			if err != nil {
				return total, err
			}
			have := map[string]bool{}
			for _, m := range existing {
				have[m.StudentID+":"+m.CompetencyID] = true
			}
			var marks []port.SeedMark
			for _, st := range students {
				// A few students struggle at the end of the year, so the closing shows recovery and repeat cases
				a := ability(st.ID)
				if hashOf("closing:"+st.ID)%9 == 0 {
					a *= 0.3
				}
				for _, c := range comps {
					if have[st.ID+":"+c.ID] {
						continue
					}
					lvl := levelFor(a, rnd, 3)
					m := port.SeedMark{SectionID: sec.ID, TermID: last.ID, CompetencyMark: domain.CompetencyMark{StudentID: st.ID, CompetencyID: c.ID, Level: lvl}}
					if lvl == "C" {
						m.Conclusion = fmt.Sprintf(conclusionsC[rnd.IntN(len(conclusionsC))], c.Name)
					}
					marks = append(marks, m)
				}
			}
			n, err := seeder.BulkCompetencyMarks(ctx, inst.ID, marks)
			total += n
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}
