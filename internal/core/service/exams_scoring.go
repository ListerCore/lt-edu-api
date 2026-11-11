package service

import (
	"math"
	"sort"
	"strings"

	"api/internal/core/domain"
)

// ScoreSheet grades the answers (one character per question: A–E or '-' blank) against the key. Each course adds
// weight × (right × pointsRight − wrong × pointsWrong); a question without key does not count
func ScoreSheet(answers string, key []domain.KeyItem, weights map[string]float64, pointsRight, pointsWrong float64) (domain.CourseScore, map[string]domain.CourseScore) {
	by := map[string]domain.CourseScore{}
	var total domain.CourseScore
	for _, k := range key {
		if k.Answer == "" {
			continue
		}
		c := by[k.CourseCode]
		mark := "-"
		if k.Number-1 < len(answers) {
			mark = string(answers[k.Number-1])
		}
		switch {
		case mark == "-" || mark == "":
			c.Blank++
		case mark == k.Answer:
			c.Right++
		default:
			c.Wrong++
		}
		by[k.CourseCode] = c
	}
	for code, c := range by {
		w, ok := weights[code]
		if !ok {
			w = 1
		}
		c.Score = round3(w * (float64(c.Right)*pointsRight - float64(c.Wrong)*pointsWrong))
		by[code] = c
		total.Right += c.Right
		total.Wrong += c.Wrong
		total.Blank += c.Blank
		total.Score += c.Score
	}
	total.Score = round3(total.Score)
	return total, by
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// NormalizeAnswers turns what a reader or a person typed into the stored form: upper case, blanks as '-', padded or
// cut to the number of questions. Anything that is not A–E counts as blank
func NormalizeAnswers(raw string, questions int) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if b.Len() >= questions {
			break
		}
		switch {
		case r >= 'A' && r <= 'E':
			b.WriteRune(r)
		case r == ' ' || r == ',' || r == ';' || r == '\t':
			// separators between answers
		default:
			b.WriteByte('-')
		}
	}
	for b.Len() < questions {
		b.WriteByte('-')
	}
	return b.String()
}

// RankSheets fills the three ranks (classroom, area, whole exam); equal scores share the place (1, 2, 2, 4)
func RankSheets(sheets []domain.AnswerSheet) {
	sort.SliceStable(sheets, func(i, j int) bool { return sheets[i].Score > sheets[j].Score })
	rank := func(group func(domain.AnswerSheet) string, set func(*domain.AnswerSheet, int)) {
		seen := map[string]int{}
		last := map[string]float64{}
		place := map[string]int{}
		for i := range sheets {
			g := group(sheets[i])
			seen[g]++
			if seen[g] == 1 || sheets[i].Score != last[g] {
				place[g] = seen[g]
			}
			last[g] = sheets[i].Score
			set(&sheets[i], place[g])
		}
	}
	rank(func(domain.AnswerSheet) string { return "" }, func(s *domain.AnswerSheet, r int) { s.RankAll = r })
	rank(func(s domain.AnswerSheet) string { return s.LevelID }, func(s *domain.AnswerSheet, r int) { s.RankLevel = r })
	rank(func(s domain.AnswerSheet) string { return s.SectionID }, func(s *domain.AnswerSheet, r int) { s.RankSection = r })
}
