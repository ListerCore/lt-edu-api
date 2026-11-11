package service

import (
	"errors"
	"testing"

	"api/internal/core/domain"
)

func TestWeightedAverage(t *testing.T) {
	evals := []domain.Evaluation{{ID: "a", Weight: 25}, {ID: "b", Weight: 25}, {ID: "c", Weight: 50}}
	if got := WeightedAverage(evals, map[string]int{}); got != nil {
		t.Fatalf("no scores must be nil, got %v", *got)
	}
	// Only taken evaluations count: (12·25 + 15·25) / 50 = 13.5
	if got := WeightedAverage(evals, map[string]int{"a": 12, "b": 15}); got == nil || *got != 13.5 {
		t.Fatalf("got %v", got)
	}
	if got := WeightedAverage(evals, map[string]int{"a": 12, "b": 15, "c": 10}); got == nil || *got != 11.8 {
		t.Fatalf("got %v", *got)
	}
	for avg, want := range map[float64]int{12.5: 13, 12.49: 12, 10.5: 11, 20: 20} {
		if RoundGrade(avg) != want {
			t.Errorf("RoundGrade(%v) = %d, want %d", avg, RoundGrade(avg), want)
		}
	}
}

func TestValidLevels(t *testing.T) {
	comps := []domain.CompetencyRef{{ID: "c1"}}
	students := []domain.StudentRef{{ID: "s1"}}
	ok := []domain.CompetencyMark{{StudentID: "s1", CompetencyID: "c1", Level: "C", Conclusion: " Necesita apoyo. "}}
	out, err := validLevels(ok, comps, students)
	if err != nil || out[0].Conclusion != "Necesita apoyo." {
		t.Fatalf("valid C rejected: %v %v", out, err)
	}
	cases := map[string][]domain.CompetencyMark{
		"s1:c1": {{StudentID: "s1", CompetencyID: "c1", Level: "C"}},
		"marks": {{StudentID: "s2", CompetencyID: "c1", Level: "A"}},
	}
	for field, marks := range cases {
		_, err := validLevels(marks, comps, students)
		var de *domain.Error
		if !errors.As(err, &de) || de.Fields[field] == "" {
			t.Errorf("want field %q, got %v", field, err)
		}
	}
	if _, err := validLevels([]domain.CompetencyMark{{StudentID: "s1", CompetencyID: "c1", Level: "E"}}, comps, students); err == nil {
		t.Error("level E accepted")
	}
}
