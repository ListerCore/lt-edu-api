package service

import (
	"testing"

	"api/internal/core/domain"
)

func TestInstituteTemplatesAre120Credits(t *testing.T) {
	for code, plan := range institutePrograms {
		total, efsrt := 0, 0
		codes := map[string]bool{}
		for _, s := range plan.subjects {
			total += s.theory + s.practice
			if s.kind == domain.SubjectEFSRT {
				efsrt++
			}
			if codes[s.code] || len(s.code) > 40 || codeFrom(s.code, "") != s.code {
				t.Fatalf("%s: bad or repeated code %q", code, s.code)
			}
			codes[s.code] = true
			if len(s.grades) != 1 || s.module == "" {
				t.Fatalf("%s/%s: a unit belongs to one semester and one module", code, s.code)
			}
			if s.hours != s.theory+2*s.practice {
				t.Fatalf("%s/%s: weekly hours do not match the credits", code, s.code)
			}
		}
		if total != 120 || efsrt != 3 {
			t.Fatalf("%s: %d credits and %d EFSRT, want 120 and 3", code, total, efsrt)
		}
	}
}

func TestCNEBTemplates(t *testing.T) {
	sec, _ := curriculumTemplateFor(domain.KindEBR, "secundaria")
	hours, competencies := 0, 0
	for _, s := range sec.subjects {
		hours += s.hours
		competencies += len(s.competencies)
		for _, c := range s.competencies {
			if c.name == "" {
				t.Fatalf("%s has a competency without name (%s)", s.code, c.code)
			}
		}
	}
	// JER secundaria: 35 hours, 6 of them left to the institution
	if hours != 29 || competencies != 26 || len(sec.transversal) != 2 {
		t.Fatalf("secundaria: %d hours, %d competencies, %d transversal", hours, competencies, len(sec.transversal))
	}
	for kind, levels := range map[string][]string{
		domain.KindEBR: {"inicial", "primaria", "secundaria"}, domain.KindEBA: {"inicial", "intermedio", "avanzado"},
		domain.KindEBE: {"prite", "inicial", "primaria"}, domain.KindCEPRE: {"uni", "unmsm-a", "unmsm-e"},
		domain.KindInstitute: {"enfermeria-tecnica", "desarrollo-sistemas", "contabilidad"},
	} {
		for _, level := range levels {
			if _, ok := curriculumTemplateFor(kind, level); !ok {
				t.Fatalf("no template for %s/%s", kind, level)
			}
		}
	}
	if _, ok := curriculumTemplateFor(domain.KindEBR, "talleres"); ok {
		t.Fatal("unknown level should have no template")
	}
	a, _ := curriculumTemplateFor(domain.KindCEPRE, "unmsm-a")
	e, _ := curriculumTemplateFor(domain.KindCEPRE, "unmsm-e")
	hoursOf := func(p curriculumTemplate, code string) int {
		for _, s := range p.subjects {
			if s.code == code {
				return s.hours
			}
		}
		return -1
	}
	if hoursOf(a, "biologia") <= hoursOf(e, "biologia") || hoursOf(e, "literatura") <= hoursOf(a, "literatura") {
		t.Fatal("San Marcos hours should follow each area's weights")
	}
}

func TestFromTemplateMapsGrades(t *testing.T) {
	level := domain.Level{ID: "L", Grades: []domain.Grade{{ID: "g1", Code: "1"}, {ID: "g3", Code: "3"}, {ID: "g6", Code: "6"}}}
	plan := fromCurriculumTemplate(ebrPrimaria, level, true)
	for _, s := range plan.Subjects {
		switch s.Code {
		case "ingles":
			if len(s.GradeIDs) != 2 || s.GradeIDs[0] != "g3" {
				t.Fatalf("inglés grades = %v", s.GradeIDs)
			}
		case "matematica":
			if len(s.GradeIDs) != 3 || s.WeeklyHours == nil || *s.WeeklyHours != 5 {
				t.Fatalf("matemática = %+v", s)
			}
		}
	}
	cepre := fromCurriculumTemplate(cepreUNI(), domain.Level{ID: "A"}, false)
	if len(cepre.Subjects[0].GradeIDs) != 0 || len(cepre.Subjects[0].Competencies) == 0 {
		t.Fatal("CEPRE courses: no grades, with topics")
	}
}

func TestCodeFromCapsLongNames(t *testing.T) {
	got := codeFrom("", "Experiencias formativas en situaciones reales de trabajo")
	if len(got) > 40 || got != "experiencias-formativas-en-situaciones" {
		t.Fatalf("codeFrom = %q", got)
	}
}
