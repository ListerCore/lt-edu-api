package service

import (
	"strings"
	"testing"

	"api/internal/core/domain"
)

func TestEBADefaultRules(t *testing.T) {
	cases := []struct {
		level string
		areas int
		name  string
	}{
		{"inicial", 4, "Ciclos inicial e intermedio: cuatro áreas"},
		{"intermedio", 4, "Ciclos inicial e intermedio: cuatro áreas"},
		{"avanzado", 3, "Ciclo avanzado: tres áreas"},
	}
	for _, c := range cases {
		r := ruleFor(domain.KindEBA, c.level, "1")
		if r.Automatic || r.Referential || r.Areas != c.areas || r.Name != c.name {
			t.Errorf("%s: %+v", c.level, r)
		}
	}
}

func TestCustomRule(t *testing.T) {
	r := customRule(domain.LevelRule{LevelName: "Ciclo Avanzado", Areas: 2})
	if r.Automatic || r.Areas != 2 || r.Name != "Ciclo Avanzado: 2 áreas (regla del CEBA)" {
		t.Fatalf("custom: %+v", r)
	}
	if r := customRule(domain.LevelRule{LevelName: "Ciclo Inicial", Automatic: true, Areas: 4}); !r.Automatic || !strings.HasPrefix(r.Name, "Promoción automática") {
		t.Fatalf("automatic: %+v", r)
	}
	// One area less needed changes the result of a student strong in only two areas
	strong2 := areas([]string{"A", "A"}, []string{"AD", "A"}, []string{"B", "B"}, []string{"B", "B"})
	if s, _ := FinalSituation(ruleFor(domain.KindEBA, "avanzado", "3"), strong2); s != SituationRecovery {
		t.Fatalf("default: %s", s)
	}
	if s, _ := FinalSituation(customRule(domain.LevelRule{Areas: 2}), strong2); s != SituationPromoted {
		t.Fatalf("custom two areas: %s", s)
	}
}

func TestReligionExemption(t *testing.T) {
	res := SectionResult{Areas: []ClosingArea{
		{SubjectID: "com", Name: "Comunicación", Competencies: []domain.CompetencyRef{{ID: "c1"}, {ID: "c2"}}},
		{SubjectID: "mat", Name: "Matemática", Competencies: []domain.CompetencyRef{{ID: "m1"}}},
		{SubjectID: "cyt", Name: "Ciencia", Competencies: []domain.CompetencyRef{{ID: "s1"}}},
		{SubjectID: "rel", SubjectCode: domain.ReligionSubjectCode, Name: "Educación Religiosa", Competencies: []domain.CompetencyRef{{ID: "r1"}, {ID: "r2"}}},
	}}
	final := map[string]string{"c1": "A", "c2": "A", "m1": "A", "s1": "AD"}
	// Without the exemption the empty Religion levels leave the student pending
	if s, _ := FinalSituation(PromotionRule{Areas: 3}, res.areaLevels(final, nil, false)); s != SituationPending {
		t.Fatalf("not exempt: %s", s)
	}
	levels := res.areaLevels(final, nil, true)
	if len(levels) != 3 {
		t.Fatalf("exempt keeps %d areas", len(levels))
	}
	if s, _ := FinalSituation(PromotionRule{Areas: 3}, levels); s != SituationPromoted {
		t.Fatalf("exempt: %s", s)
	}
}

func TestBasicRecordMarksExemption(t *testing.T) {
	res := SectionResult{
		Section: domain.SectionMeta{Label: "Multigrado A", LevelName: "Ciclo Avanzado"},
		Areas: []ClosingArea{
			{SubjectID: "mat", Name: "Matemática", Competencies: []domain.CompetencyRef{{ID: "m1", Name: "Resuelve"}}},
			{SubjectID: "rel", SubjectCode: domain.ReligionSubjectCode, Name: "Educación Religiosa", Competencies: []domain.CompetencyRef{{ID: "r1", Name: "Conoce"}}},
		},
		Rows: []ClosingRow{
			{Student: domain.StudentRef{ID: "a", Name: "Ccori, Ana"}, Final: map[string]string{"m1": "A", "r1": "B"}, Situation: SituationPromoted, Confirmed: true, GradeShort: "3.°"},
			{Student: domain.StudentRef{ID: "b", Name: "Mamani, Luis"}, Final: map[string]string{"m1": "A"}, Situation: SituationPromoted, Confirmed: true, GradeShort: "4.°", ReligionExempt: true},
		},
	}
	c, err := basicRecord(gradeScope{}, res, "final", "2026-12-15")
	if err != nil {
		t.Fatal(err)
	}
	if c.Rows[0].Grade != "3.°" || c.Rows[1].Grade != "4.°" {
		t.Fatalf("grades: %+v", c.Rows)
	}
	if c.Rows[0].Cells[1] != "B" || c.Rows[1].Cells[1] != Exempt {
		t.Fatalf("cells: %v %v", c.Rows[0].Cells, c.Rows[1].Cells)
	}
	if !strings.Contains(strings.Join(c.Legend, " "), "EXO") {
		t.Fatalf("legend: %v", c.Legend)
	}
}

func TestConcludedCycles(t *testing.T) {
	studies := []domain.StudyRecord{
		{LevelID: "ini", GradeShort: "1.°", Confirmed: true, Situation: SituationPromoted},
		{LevelID: "ini", GradeShort: "2.°", LastGrade: true, Confirmed: true, Situation: SituationPromoted},
		{LevelID: "int", GradeShort: "3.°", LastGrade: true, Confirmed: true, Situation: SituationRepeat},
		{LevelID: "ava", GradeShort: "4.°", LastGrade: true, Situation: SituationPromoted},
	}
	got := concludedCycles(studies)
	if len(got) != 1 || got[0].LevelID != "ini" {
		t.Fatalf("concluded: %+v", got)
	}
}

func TestStudyResult(t *testing.T) {
	cases := []struct {
		r    domain.StudyRecord
		want string
	}{
		{domain.StudyRecord{Status: domain.EnrollmentActive}, "En curso"},
		{domain.StudyRecord{Status: domain.EnrollmentCompleted, Confirmed: true, Situation: SituationPromoted}, "Promovido"},
		{domain.StudyRecord{Status: domain.EnrollmentActive, Confirmed: true, Situation: SituationRecovery}, "Recuperación pedagógica"},
		{domain.StudyRecord{Status: domain.EnrollmentWithdrawn, Confirmed: true, Situation: SituationRepeat}, "Retirado"},
	}
	for _, c := range cases {
		if got := studyResult(c.r); got != c.want {
			t.Errorf("%+v: %s, want %s", c.r, got, c.want)
		}
	}
}
