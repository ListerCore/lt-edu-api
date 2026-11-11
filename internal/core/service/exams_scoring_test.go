package service

import (
	"testing"

	"api/internal/core/domain"
)

func TestScoreSheet(t *testing.T) {
	key := []domain.KeyItem{{Number: 1, CourseCode: "alg", Answer: "A"}, {Number: 2, CourseCode: "alg", Answer: "B"}, {Number: 3, CourseCode: "bio", Answer: "C"}, {Number: 4, CourseCode: "bio", Answer: ""}}
	total, by := ScoreSheet("AC-D", key, map[string]float64{"alg": 2}, 20, 1.125)
	if total.Right != 1 || total.Wrong != 1 || total.Blank != 1 {
		t.Fatalf("counts %+v", total)
	}
	// alg: 2 × (20 − 1.125) = 37.75; bio: blank = 0; question 4 has no key
	if by["alg"].Score != 37.75 || by["bio"].Score != 0 || total.Score != 37.75 {
		t.Fatalf("scores %+v %+v", by, total)
	}
	if total, _ := ScoreSheet("", key, nil, 20, 1.125); total.Blank != 3 || total.Score != 0 {
		t.Fatalf("empty sheet %+v", total)
	}
}

func TestNormalizeAnswers(t *testing.T) {
	cases := map[string]string{"abc": "ABC--", "A B,C;d e f": "ABCDE", "A*C": "A-C--", "  ": "-----"}
	for in, want := range cases {
		if got := NormalizeAnswers(in, 5); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestRankSheets(t *testing.T) {
	s := []domain.AnswerSheet{
		{StudentID: "a", Score: 50, SectionID: "x", LevelID: "l1"},
		{StudentID: "b", Score: 80, SectionID: "y", LevelID: "l1"},
		{StudentID: "c", Score: 80, SectionID: "x", LevelID: "l2"},
		{StudentID: "d", Score: 70, SectionID: "x", LevelID: "l1"},
	}
	RankSheets(s)
	got := map[string][3]int{}
	for _, x := range s {
		got[x.StudentID] = [3]int{x.RankAll, x.RankLevel, x.RankSection}
	}
	want := map[string][3]int{"b": {1, 1, 1}, "c": {1, 1, 1}, "d": {3, 2, 2}, "a": {4, 3, 3}}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v want %v", k, got[k], v)
		}
	}
}

func TestParseReaderFile(t *testing.T) {
	text := "\xEF\xBB\xBFcodigo;respuestas\n2026-0001;ABCDE\n2026-0002;A;;C;D;E\n\n"
	got, err := ParseReaderFile(text)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v %v", got, err)
	}
	if got[0].Code != "2026-0001" || got[0].Answers != "ABCDE" || got[1].Answers != "A-CDE" {
		t.Fatalf("parsed %+v", got)
	}
	if _, err := ParseReaderFile("solo texto"); err == nil {
		t.Fatal("a file without rows must fail")
	}
}

func TestStructureFromHours(t *testing.T) {
	courses := []domain.Course{
		{Code: "bio", Name: "Biología", Hours: map[string]int{"a": 4, "e": 1}},
		{Code: "his", Name: "Historia", Hours: map[string]int{"a": 1, "e": 3}},
		{Code: "fis", Name: "Física", Hours: map[string]int{"u": 5}},
	}
	c, w := structureFromHours(courses, []string{"a", "e"}, 20)
	total := 0
	for _, x := range c {
		total += x.Questions
	}
	if len(c) != 2 || total != 20 {
		t.Fatalf("courses %+v", c)
	}
	weight := map[string]float64{}
	for _, x := range w {
		weight[x.LevelID+x.CourseCode] = x.Weight
	}
	// Biología: 4 h in area a vs an average of 2.5 → 1.5; 1 h in area e → 0.5 (rounded to quarters)
	if weight["abio"] != 1.5 || weight["ebio"] != 0.5 {
		t.Fatalf("weights %v", weight)
	}
}
