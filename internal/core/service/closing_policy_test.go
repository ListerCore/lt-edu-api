package service

import (
	"testing"

	"api/internal/core/domain"
)

func areas(levels ...[]string) []AreaLevels {
	out := make([]AreaLevels, len(levels))
	for i, l := range levels {
		out[i] = AreaLevels{Name: string(rune('A' + i)), Levels: l}
	}
	return out
}

func TestRuleFor(t *testing.T) {
	cases := []struct {
		kind, level, grade string
		auto               bool
		areas              int
	}{
		{domain.KindEBR, "inicial", "5-anos", true, 0},
		{domain.KindEBR, "primaria", "1", true, 0},
		{domain.KindEBR, "primaria", "2", false, 4},
		{domain.KindEBR, "secundaria", "1", false, 3},
		{domain.KindEBA, "inicial", "1", false, 4},
		{domain.KindEBA, "avanzado", "3", false, 3},
		{domain.KindEBE, "prite", "0-1", true, 0},
	}
	for _, c := range cases {
		r := ruleFor(c.kind, c.level, c.grade)
		if r.Automatic != c.auto || r.Areas != c.areas {
			t.Errorf("%s %s %s: %+v", c.kind, c.level, c.grade, r)
		}
	}
	if !ruleFor(domain.KindEBE, "primaria", "3").Referential {
		t.Error("EBE must be referential")
	}
}

func TestFinalSituation(t *testing.T) {
	sec := PromotionRule{Areas: 3}
	cases := []struct {
		name  string
		areas []AreaLevels
		want  string
	}{
		{"all A", areas([]string{"A", "A"}, []string{"AD", "B"}, []string{"A", "B", "A"}, []string{"B", "B"}), SituationPromoted},
		{"three areas half A, rest B", areas([]string{"A", "B"}, []string{"A", "B"}, []string{"AD", "B"}, []string{"B", "B"}), SituationPromoted},
		{"a C blocks promotion", areas([]string{"A", "A"}, []string{"A", "A"}, []string{"A", "A"}, []string{"B", "C"}), SituationRecovery},
		{"only two strong areas", areas([]string{"A", "A"}, []string{"A", "A"}, []string{"B", "B"}, []string{"B", "B"}), SituationRecovery},
		{"mostly C in three areas", areas([]string{"C", "C"}, []string{"C", "C", "B"}, []string{"C", "C"}, []string{"A", "B"}), SituationRepeat},
		{"half C is not more than half", areas([]string{"C", "B"}, []string{"C", "B"}, []string{"C", "B"}), SituationRecovery},
		{"missing level", areas([]string{"A", ""}, []string{"A", "A"}, []string{"A", "A"}), SituationPending},
	}
	for _, c := range cases {
		if got, reason := FinalSituation(sec, c.areas); got != c.want {
			t.Errorf("%s: got %s (%s), want %s", c.name, got, reason, c.want)
		}
	}
	if got, _ := FinalSituation(PromotionRule{Automatic: true}, areas([]string{"C"})); got != SituationPromoted {
		t.Error("automatic rule must promote")
	}
	if got, _ := FinalSituation(PromotionRule{Areas: 4}, areas([]string{"A"}, []string{"A"}, []string{"A"})); got != SituationRecovery {
		t.Error("primaria needs four areas")
	}
	if got, _ := AfterRecovery(sec, areas([]string{"A", "B"}, []string{"A", "B"}, []string{"A", "B"})); got != SituationPromoted {
		t.Error("recovered student must be promoted")
	}
	if got, _ := AfterRecovery(sec, areas([]string{"A", "C"}, []string{"A", "A"}, []string{"A", "A"})); got != SituationRepeat {
		t.Error("a C after recovery means repeat")
	}
}

func TestUnitStatus(t *testing.T) {
	g := func(v int) *int { return &v }
	cases := []struct {
		grade  *int
		absent float64
		want   string
	}{
		{g(13), 0, UnitApproved}, {g(12), 0, UnitRecovery}, {g(5), 0, UnitRecovery}, {nil, 0, UnitFailed}, {g(18), 30, UnitBarred},
	}
	for _, c := range cases {
		if got := UnitStatus(c.grade, 13, c.absent, 30); got != c.want {
			t.Errorf("%v %v: got %s want %s", c.grade, c.absent, got, c.want)
		}
	}
	if f, s := UnitAfterRecovery(g(11), 14, 13); f != 14 || s != UnitApproved {
		t.Errorf("recovery 14: %d %s", f, s)
	}
	if f, s := UnitAfterRecovery(g(11), 9, 13); f != 11 || s != UnitFailed {
		t.Errorf("recovery 9: %d %s", f, s)
	}
}
