package service

import (
	"fmt"
	"strings"

	"api/internal/core/domain"
)

// Final situations of Educación Básica (RVM 094-2020-MINEDU) and the pending one while levels are missing
const (
	SituationPromoted = "PRO"
	SituationRepeat   = "PER"
	SituationRecovery = "RR"
	SituationPending  = "PEN"
)

// PromotionRule is how a grade decides the final situation
type PromotionRule struct {
	// Promotion without conditions (Inicial, 1.° de primaria)
	Automatic bool
	// Areas whose competencies must be mostly A/AD (promotion) or mostly C (repeat)
	Areas int
	// EBE: the result only guides the team, who decides with the student's individual plan (POI)
	Referential bool
	Name        string
}

// ruleFor picks the rule of a level and grade, by institution kind
func ruleFor(kind, levelCode, gradeCode string) PromotionRule {
	level := strings.ToLower(levelCode)
	switch {
	case level == "inicial" && kind != domain.KindEBA, level == "prite":
		return PromotionRule{Automatic: true, Name: "Promoción automática en Educación Inicial"}
	case level == "primaria" && gradeCode == "1" && kind == domain.KindEBR:
		return PromotionRule{Automatic: true, Name: "Promoción automática en 1.° de primaria"}
	case kind == domain.KindEBA && level == "avanzado":
		return PromotionRule{Areas: 3, Name: "Ciclo avanzado: tres áreas"}
	case kind == domain.KindEBA:
		return PromotionRule{Areas: 4, Name: "Ciclos inicial e intermedio: cuatro áreas"}
	case level == "secundaria", level == "avanzado":
		return PromotionRule{Areas: 3, Referential: kind == domain.KindEBE, Name: "Secundaria: tres áreas"}
	default:
		return PromotionRule{Areas: 4, Referential: kind == domain.KindEBE, Name: "Primaria: cuatro áreas"}
	}
}

// AreaLevels are the final levels of the competencies of one area ("" when missing)
type AreaLevels struct {
	Name   string
	Levels []string
}

// FinalSituation applies the rule to the final levels of the areas (transversal competencies do not count):
//   - PRO: A or AD in half or more of the competencies of at least N areas, and no C anywhere
//   - PER: C in more than half of the competencies of at least N areas
//   - RR: anything else (pedagogical recovery)
func FinalSituation(rule PromotionRule, areas []AreaLevels) (string, string) {
	if rule.Automatic {
		return SituationPromoted, rule.Name + "."
	}
	missing := 0
	for _, a := range areas {
		for _, l := range a.Levels {
			if l == "" {
				missing++
			}
		}
	}
	if missing > 0 {
		return SituationPending, fmt.Sprintf("Faltan %d %s del último periodo.", missing, plural(missing, "nivel", "niveles"))
	}
	high, low := 0, 0
	var withC []string
	for _, a := range areas {
		if len(a.Levels) == 0 {
			continue
		}
		good, c := 0, 0
		for _, l := range a.Levels {
			switch l {
			case "AD", "A":
				good++
			case "C":
				c++
			}
		}
		if good*2 >= len(a.Levels) {
			high++
		}
		if c*2 > len(a.Levels) {
			low++
		}
		if c > 0 {
			withC = append(withC, a.Name)
		}
	}
	switch {
	case high >= rule.Areas && len(withC) == 0:
		return SituationPromoted, fmt.Sprintf("A o AD en la mitad o más de las competencias de %d %s, sin C.", high, plural(high, "área", "áreas"))
	case low >= rule.Areas:
		return SituationRepeat, fmt.Sprintf("C en más de la mitad de las competencias de %d %s.", low, plural(low, "área", "áreas"))
	case len(withC) > 0:
		return SituationRecovery, "Va a recuperación en " + strings.Join(withC, ", ") + "."
	default:
		return SituationRecovery, fmt.Sprintf("A o AD en la mayoría de competencias de solo %d %s (se piden %d).", high, plural(high, "área", "áreas"), rule.Areas)
	}
}

// AfterRecovery decides the definitive situation once the recovery levels replace the final ones: promotion if the
// rule is met now, otherwise the student stays in the grade
func AfterRecovery(rule PromotionRule, areas []AreaLevels) (string, string) {
	s, reason := FinalSituation(rule, areas)
	if s == SituationPromoted {
		return SituationPromoted, "Tras la recuperación: " + strings.ToLower(reason[:1]) + reason[1:]
	}
	return SituationRepeat, "Tras la recuperación no alcanza las condiciones de promoción."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Unit results of IEST (and courses with scores)
const (
	UnitApproved = "approved"
	UnitRecovery = "recovery"
	UnitFailed   = "failed"
	UnitBarred   = "barred"
)

// UnitStatus judges a unit: too many absences bar it; a whole grade at or above the passing one approves it; any
// other failing grade can go to recovery (no scores at all: failed)
func UnitStatus(grade *int, passing int, absentPercent float64, limit int) string {
	switch {
	case absentPercent >= float64(limit):
		return UnitBarred
	case grade == nil:
		return UnitFailed
	case *grade >= passing:
		return UnitApproved
	default:
		return UnitRecovery
	}
}

// UnitAfterRecovery: the final grade is the best of the unit and the recovery
func UnitAfterRecovery(grade *int, recovery int, passing int) (int, string) {
	final := recovery
	if grade != nil && *grade > final {
		final = *grade
	}
	if final >= passing {
		return final, UnitApproved
	}
	return final, UnitFailed
}
