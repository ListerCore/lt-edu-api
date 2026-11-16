package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestAutoGrade(t *testing.T) {
	qs := []domain.QuizQuestion{
		{ID: "s", Kind: domain.QuestionSingle, Options: []string{"1/2", "1/4"}, Correct: []int{0}, Points: 2},
		{ID: "m", Kind: domain.QuestionMultiple, Options: []string{"a", "b", "c"}, Correct: []int{0, 2}, Points: 3},
		{ID: "t", Kind: domain.QuestionShort, Accepted: []string{"Perú", "República del Perú"}, Points: 1},
		{ID: "o", Kind: domain.QuestionOpen, Points: 5},
	}
	cases := []struct {
		name    string
		answers map[string]domain.QuizAnswer
		points  int
	}{
		{"all right", map[string]domain.QuizAnswer{"s": {Choices: []int{0}}, "m": {Choices: []int{2, 0}}, "t": {Text: "  peru "}}, 6},
		{"multiple needs every right option", map[string]domain.QuizAnswer{"m": {Choices: []int{0}}}, 0},
		{"multiple with a wrong option", map[string]domain.QuizAnswer{"m": {Choices: []int{0, 1, 2}}}, 0},
		{"repeated choices count once", map[string]domain.QuizAnswer{"m": {Choices: []int{0, 0, 2}}}, 3},
		{"short ignores case and accents", map[string]domain.QuizAnswer{"t": {Text: "REPÚBLICA DEL  PERU"}}, 1},
		{"empty answers", map[string]domain.QuizAnswer{}, 0},
	}
	for _, c := range cases {
		points, total := autoGrade(qs, c.answers)
		if total != 6 {
			t.Fatalf("%s: total %d, open questions must not count", c.name, total)
		}
		if points != c.points {
			t.Errorf("%s: %d points, want %d", c.name, points, c.points)
		}
	}
}

func TestSuggestedLevel(t *testing.T) {
	for _, c := range []struct {
		points, total int
		want          string
	}{{9, 10, "AD"}, {7, 10, "A"}, {69, 100, "B"}, {5, 10, "B"}, {4, 10, "C"}, {0, 0, ""}} {
		if got := SuggestedLevel(c.points, c.total); got != c.want {
			t.Errorf("%d/%d: %q, want %q", c.points, c.total, got, c.want)
		}
	}
}

func TestAssessmentWindowWithExtension(t *testing.T) {
	opens := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	closes := opens.AddDate(0, 0, 7)
	a := domain.Assessment{OpensAt: &opens, ClosesAt: &closes}
	later := closes.Add(48 * time.Hour)
	if a.Open(opens.Add(-time.Minute), nil) || !a.Open(opens, nil) || !a.Open(closes, nil) || a.Open(closes.Add(time.Minute), nil) {
		t.Fatal("window must be [opens, closes]")
	}
	if !a.Open(closes.Add(time.Hour), &later) {
		t.Fatal("an extension keeps it open for that student")
	}
	if (domain.Assessment{}).Open(opens, nil) != true {
		t.Fatal("no dates: always open")
	}
}

func TestValidateQuestions(t *testing.T) {
	cases := []struct {
		name  string
		q     domain.QuizQuestion
		valid bool
	}{
		{"single", domain.QuizQuestion{Kind: "single", Prompt: "¿1/2 + 1/2?", Options: []string{"1", "2", " "}, Correct: []int{0}}, true},
		{"single needs exactly one", domain.QuizQuestion{Kind: "single", Prompt: "?", Options: []string{"a", "b"}, Correct: []int{0, 1}}, false},
		{"needs two options", domain.QuizQuestion{Kind: "multiple", Prompt: "?", Options: []string{"a"}, Correct: []int{0}}, false},
		{"correct must be an option", domain.QuizQuestion{Kind: "multiple", Prompt: "?", Options: []string{"a", "b"}, Correct: []int{5}}, false},
		{"short needs an answer", domain.QuizQuestion{Kind: "short", Prompt: "Capital"}, false},
		{"open", domain.QuizQuestion{Kind: "open", Prompt: "Explica", Points: 5}, true},
		{"prompt required", domain.QuizQuestion{Kind: "open", Prompt: " "}, false},
		{"points range", domain.QuizQuestion{Kind: "open", Prompt: "x", Points: 101}, false},
	}
	for _, c := range cases {
		qs := []domain.QuizQuestion{c.q}
		fields := validateQuestions(qs)
		if (len(fields) == 0) != c.valid {
			t.Errorf("%s: valid=%v, errors %v", c.name, c.valid, fields)
		}
	}
	// Blank options go away and the default value is one point
	qs := []domain.QuizQuestion{{Kind: "single", Prompt: "?", Options: []string{"a", "", "b"}, Correct: []int{1}}}
	validateQuestions(qs)
	if len(qs[0].Options) != 2 || qs[0].Points != 1 {
		t.Fatalf("cleaned: %+v", qs[0])
	}
}

func TestCleanAnswers(t *testing.T) {
	a := domain.Assessment{Questions: []domain.QuizQuestion{
		{ID: "s", Kind: "single", Options: []string{"a", "b"}},
		{ID: "m", Kind: "multiple", Options: []string{"a", "b", "c"}},
		{ID: "o", Kind: "open"},
	}}
	got := cleanAnswers(a, map[string]domain.QuizAnswer{
		"s":     {Choices: []int{1, 0}},
		"m":     {Choices: []int{2, 2, 9}},
		"o":     {Text: "  mi respuesta  ", Choices: []int{1}},
		"other": {Text: "x"},
	})
	if len(got) != 3 || len(got["s"].Choices) != 1 || len(got["m"].Choices) != 1 || got["o"].Text != "mi respuesta" || got["o"].Choices != nil {
		t.Fatalf("clean: %+v", got)
	}
}
