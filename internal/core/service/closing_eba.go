package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"api/internal/core/domain"
)

// Closing of the EBA promotional period (phase E7 of docs/ceba/PLAN.md): the CEBA sets the promotion rule of each
// cycle (decision D4: the norm may change, so the rule is data and not code) and the SIAGIE loading state of the
// final grades of each student

// Exempt is the cell of an area the student is exempted from (Educación Religiosa, Ley 29635)
const Exempt = "EXO"

// rule is the promotion rule of a section: the one the CEBA set for its cycle, or the one of the code
func (s *ClosingService) rule(ctx context.Context, sc gradeScope, level domain.SectionLevel) (PromotionRule, error) {
	base := ruleFor(sc.institution.Kind, level.LevelCode, level.GradeCode)
	if sc.institution.Kind != domain.KindEBA {
		return base, nil
	}
	rules, err := s.repo.PromotionRules(ctx, sc.institution.ID)
	if err != nil {
		return base, err
	}
	for _, r := range rules {
		if r.LevelCode == level.LevelCode && r.Custom {
			return customRule(r), nil
		}
	}
	return base, nil
}

func customRule(r domain.LevelRule) PromotionRule {
	if r.Automatic {
		return PromotionRule{Automatic: true, Name: "Promoción automática en el " + r.LevelName + " (regla del CEBA)"}
	}
	return PromotionRule{Areas: r.Areas, Name: fmt.Sprintf("%s: %d %s (regla del CEBA)", r.LevelName, r.Areas, plural(r.Areas, "área", "áreas"))}
}

// LevelRuleView is the rule of a cycle with the default of the code next to it
type LevelRuleView struct {
	domain.LevelRule
	DefaultName      string
	DefaultAreas     int
	DefaultAutomatic bool
}

type PromotionRules struct {
	Rules     []LevelRuleView
	CanManage bool
}

func (s *ClosingService) ebaScope(ctx context.Context, token, slug string, write bool) (gradeScope, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, err
	}
	if sc.institution.Kind != domain.KindEBA {
		return sc, &domain.Error{Kind: domain.KindInvalid, Code: "not_eba", Message: "Esta función es propia de la Educación Básica Alternativa."}
	}
	if write && !sc.manager() {
		return sc, domain.ErrClosingForbidden
	}
	return sc, nil
}

// PromotionRules lists the rule of each cycle of the CEBA
func (s *ClosingService) PromotionRules(ctx context.Context, token, slug string) (PromotionRules, error) {
	sc, err := s.ebaScope(ctx, token, slug, false)
	if err != nil {
		return PromotionRules{}, err
	}
	rules, err := s.repo.PromotionRules(ctx, sc.institution.ID)
	if err != nil {
		return PromotionRules{}, err
	}
	out := PromotionRules{CanManage: sc.manager()}
	for _, r := range rules {
		def := ruleFor(sc.institution.Kind, r.LevelCode, "")
		v := LevelRuleView{LevelRule: r, DefaultName: def.Name, DefaultAreas: def.Areas, DefaultAutomatic: def.Automatic}
		if !r.Custom {
			v.Automatic, v.Areas = def.Automatic, def.Areas
		}
		out.Rules = append(out.Rules, v)
	}
	return out, nil
}

// SavePromotionRule sets the rule of a cycle; it applies to the sections not confirmed yet (confirmed results keep
// the situation they were stored with)
func (s *ClosingService) SavePromotionRule(ctx context.Context, token, slug string, r domain.LevelRule) (PromotionRules, error) {
	sc, err := s.ebaScope(ctx, token, slug, true)
	if err != nil {
		return PromotionRules{}, err
	}
	r.Note = strings.TrimSpace(r.Note)
	switch {
	case !r.Automatic && (r.Areas < 1 || r.Areas > 12):
		return PromotionRules{}, domain.Validation(map[string]string{"areas": "Indica entre 1 y 12 áreas."})
	case len([]rune(r.Note)) > 300:
		return PromotionRules{}, domain.Validation(map[string]string{"note": "La nota va hasta 300 caracteres."})
	}
	rules, err := s.repo.PromotionRules(ctx, sc.institution.ID)
	if err != nil {
		return PromotionRules{}, err
	}
	i := slices.IndexFunc(rules, func(x domain.LevelRule) bool { return x.LevelID == r.LevelID })
	if i < 0 {
		return PromotionRules{}, domain.ErrNotFound
	}
	if r.Automatic {
		r.Areas = max(r.Areas, ruleFor(sc.institution.Kind, rules[i].LevelCode, "").Areas, 1)
	}
	if err := s.repo.SavePromotionRule(ctx, sc.institution.ID, r, s.assessment.userName(sc)); err != nil {
		return PromotionRules{}, err
	}
	r.LevelName = rules[i].LevelName
	s.audit(ctx, sc, AuditEntry{Action: domain.AuditUpdate, Entity: "promotion_rule", EntityID: r.LevelID,
		Summary: "Fijó la regla de promoción: " + strings.TrimSuffix(customRule(r).Name, " (regla del CEBA)")})
	s.changed(ctx, sc, "")
	return s.PromotionRules(ctx, token, slug)
}

// ResetPromotionRule brings a cycle back to the rule of the code
func (s *ClosingService) ResetPromotionRule(ctx context.Context, token, slug, levelID string) (PromotionRules, error) {
	sc, err := s.ebaScope(ctx, token, slug, true)
	if err != nil {
		return PromotionRules{}, err
	}
	if err := s.repo.ResetPromotionRule(ctx, sc.institution.ID, levelID); err != nil {
		return PromotionRules{}, err
	}
	s.audit(ctx, sc, AuditEntry{Action: domain.AuditUpdate, Entity: "promotion_rule", EntityID: levelID, Summary: "Volvió a la regla de promoción por defecto"})
	s.changed(ctx, sc, "")
	return s.PromotionRules(ctx, token, slug)
}

func (s *ClosingService) audit(ctx context.Context, sc gradeScope, e AuditEntry) {
	e.Module = "closing"
	s.assessment.academic.audit.Record(ctx, sc.institutionScope, e)
}

// siagieWriters load the grades in the SIAGIE: the secretariat along with the managers
func siagieWriter(sc gradeScope) bool { return sc.manager() || sc.role == "secretary" }

// SetSiagieGrades marks the loading state of the final grades of some students of a section (observed needs the
// reason the SIAGIE gave)
func (s *ClosingService) SetSiagieGrades(ctx context.Context, token, slug, sectionID string, studentIDs []string, status, note string) (SectionResult, error) {
	sc, err := s.ebaScope(ctx, token, slug, false)
	if err != nil {
		return SectionResult{}, err
	}
	if !siagieWriter(sc) {
		return SectionResult{}, domain.ErrClosingForbidden
	}
	note = strings.TrimSpace(note)
	switch {
	case !siagieStates[status]:
		return SectionResult{}, domain.Validation(map[string]string{"status": "Elige pendiente, exportado u observado."})
	case status == domain.SiagieObserved && note == "":
		return SectionResult{}, domain.ErrSiagieNoteRequired
	case len([]rune(note)) > 300:
		return SectionResult{}, domain.Validation(map[string]string{"note": "La nota va hasta 300 caracteres."})
	}
	if status != domain.SiagieObserved {
		note = ""
	}
	res, err := s.Section(ctx, token, slug, sectionID)
	if err != nil {
		return res, err
	}
	n, err := s.markSiagie(ctx, sc, res, studentIDs, status, note)
	if err != nil {
		return res, err
	}
	s.audit(ctx, sc, AuditEntry{Action: domain.AuditUpdate, Entity: "siagie_grades", EntityID: sectionID,
		Summary: fmt.Sprintf("Marcó %d %s de %s como %s en el SIAGIE", n, plural(n, "estudiante", "estudiantes"), res.Section.Label, siagieGradeNames[status])})
	s.changed(ctx, sc, sectionID)
	return s.Section(ctx, token, slug, sectionID)
}

var siagieGradeNames = map[string]string{domain.SiagiePending: "pendiente", domain.SiagieExported: "exportado", domain.SiagieObserved: "observado"}

// markSiagie sets the state of the students of the section given (every one when none)
func (s *ClosingService) markSiagie(ctx context.Context, sc gradeScope, res SectionResult, studentIDs []string, status, note string) (int, error) {
	var ids []string
	for _, r := range res.Rows {
		if r.EnrollmentID != "" && (len(studentIDs) == 0 || slices.Contains(studentIDs, r.Student.ID)) {
			ids = append(ids, r.EnrollmentID)
		}
	}
	if len(ids) == 0 {
		return 0, domain.ErrNotFound
	}
	return s.repo.SetSiagieGrades(ctx, sc.institution.ID, ids, status, note, s.assessment.userName(sc), s.assessment.attendance.now())
}
