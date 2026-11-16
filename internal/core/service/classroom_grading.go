package service

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"api/internal/core/domain"
)

// normalizeAnswer compares short answers the way a teacher would: no case, accents or extra spaces
func normalizeAnswer(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.Join(strings.Fields(strings.ToLower(out)), " ")
}

// autoGrade scores the questions a machine can check: single and multiple choice (all and only the right options)
// and short answers (any accepted one). Open questions are left to the teacher and do not count in total
func autoGrade(questions []domain.QuizQuestion, answers map[string]domain.QuizAnswer) (points, total int) {
	for _, q := range questions {
		if q.Kind == domain.QuestionOpen {
			continue
		}
		total += q.Points
		a := answers[q.ID]
		switch q.Kind {
		case domain.QuestionSingle, domain.QuestionMultiple:
			got := slices.Clone(a.Choices)
			want := slices.Clone(q.Correct)
			slices.Sort(got)
			slices.Sort(want)
			if len(got) > 0 && slices.Equal(slices.Compact(got), want) {
				points += q.Points
			}
		case domain.QuestionShort:
			given := normalizeAnswer(a.Text)
			if given == "" {
				continue
			}
			for _, acc := range q.Accepted {
				if normalizeAnswer(acc) == given {
					points += q.Points
					break
				}
			}
		}
	}
	return points, total
}

// SuggestedLevel proposes an achievement level from a share of points; the teacher always confirms it.
// AD from 90 %, A from 70 %, B from 50 %, C below; "" when nothing was scored
func SuggestedLevel(points, total int) string {
	if total <= 0 {
		return ""
	}
	pct := points * 100 / total
	switch {
	case pct >= 90:
		return "AD"
	case pct >= 70:
		return "A"
	case pct >= 50:
		return "B"
	default:
		return "C"
	}
}
