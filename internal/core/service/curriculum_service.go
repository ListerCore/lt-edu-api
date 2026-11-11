package service

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Subject kinds each institution kind may use, the first being the default
var subjectKindsFor = map[string][]string{
	domain.KindEBR:       {domain.SubjectArea, domain.SubjectWorkshop, domain.SubjectTutoring},
	domain.KindEBA:       {domain.SubjectArea, domain.SubjectWorkshop, domain.SubjectTutoring},
	domain.KindEBE:       {domain.SubjectArea, domain.SubjectWorkshop, domain.SubjectTutoring},
	domain.KindCEPRE:     {domain.SubjectCourse},
	domain.KindInstitute: {domain.SubjectUnit, domain.SubjectEFSRT},
}

// What is assessed in each kind of institution
var competencyKindFor = map[string]string{
	domain.KindEBR: domain.CompetencyKind, domain.KindEBA: domain.CompetencyKind, domain.KindEBE: domain.CompetencyKind,
	domain.KindCEPRE: domain.TopicKind, domain.KindInstitute: domain.CapacityKind,
}

// CurriculumService runs the study plans (phase 2 of docs/plan-academico.md): one or more versions per level, at
// most one in force, with their areas and competencies, CEPRE courses and topics, or institute modules, didactic
// units, credits and capacities. Same permissions as the academic structure
type CurriculumService struct {
	repo     port.CurriculumRepository
	academic *AcademicService
}

func NewCurriculumService(repo port.CurriculumRepository, academic *AcademicService) *CurriculumService {
	return &CurriculumService{repo: repo, academic: academic}
}

// CurriculaOverview is the plans page: the levels (with their grades) and every plan of the institution
type CurriculaOverview struct {
	Context   AcademicContext
	Levels    []domain.Level
	Curricula []domain.Curriculum
	// Level ids that have a suggested plan to start from
	TemplateLevels []string
}

// CurriculumDetail is one plan with the level it belongs to
type CurriculumDetail struct {
	Context    AcademicContext
	Curriculum domain.Curriculum
	Level      domain.Level
}

func (s *CurriculumService) Overview(ctx context.Context, token, slug string) (CurriculaOverview, error) {
	sc, err := s.academic.scope(ctx, token, slug, false)
	if err != nil {
		return CurriculaOverview{}, err
	}
	actx, err := s.academic.contextFor(ctx, sc)
	if err != nil {
		return CurriculaOverview{}, err
	}
	levels, err := s.academic.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return CurriculaOverview{}, err
	}
	curricula, err := s.repo.Curricula(ctx, sc.institution.ID)
	if err != nil {
		return CurriculaOverview{}, err
	}
	var withTemplate []string
	for _, l := range levels {
		if _, ok := curriculumTemplateFor(sc.institution.Kind, l.Code); ok {
			withTemplate = append(withTemplate, l.ID)
		}
	}
	return CurriculaOverview{Context: actx, Levels: levels, Curricula: curricula, TemplateLevels: withTemplate}, nil
}

func (s *CurriculumService) Get(ctx context.Context, token, slug, id string) (CurriculumDetail, error) {
	sc, err := s.academic.scope(ctx, token, slug, false)
	if err != nil {
		return CurriculumDetail{}, err
	}
	return s.detail(ctx, sc, id)
}

func (s *CurriculumService) detail(ctx context.Context, sc academicScope, id string) (CurriculumDetail, error) {
	actx, err := s.academic.contextFor(ctx, sc)
	if err != nil {
		return CurriculumDetail{}, err
	}
	c, err := s.repo.Curriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return CurriculumDetail{}, err
	}
	level, err := s.academic.repo.Level(ctx, sc.institution.ID, c.LevelID)
	if err != nil {
		return CurriculumDetail{}, err
	}
	return CurriculumDetail{Context: actx, Curriculum: c, Level: level}, nil
}

func validatePlanFields(name, notes string) map[string]string {
	fields := map[string]string{}
	if n := utf8.RuneCountInString(strings.TrimSpace(name)); n < 3 || n > 80 {
		fields["name"] = "Escribe un nombre de 3 a 80 caracteres."
	}
	if utf8.RuneCountInString(notes) > 1000 {
		fields["notes"] = "Máximo 1000 caracteres."
	}
	return fields
}

// Create starts a draft plan for a level, empty or from the suggested plan of its kind
func (s *CurriculumService) Create(ctx context.Context, token, slug, levelID, name string, fromTemplate bool) (domain.Curriculum, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Curriculum{}, err
	}
	level, err := s.academic.repo.Level(ctx, sc.institution.ID, levelID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Curriculum{}, domain.Validation(map[string]string{"levelId": "Elige a qué corresponde el plan."})
		}
		return domain.Curriculum{}, err
	}
	plan := domain.Curriculum{LevelID: level.ID, Status: domain.CurriculumDraft, Name: strings.TrimSpace(name)}
	if fromTemplate {
		t, ok := curriculumTemplateFor(sc.institution.Kind, level.Code)
		if !ok {
			return domain.Curriculum{}, &domain.Error{Kind: domain.KindInvalid, Code: "no_template", Message: "No hay un plan sugerido para " + level.Name + ". Empieza con uno vacío."}
		}
		plan = fromCurriculumTemplate(t, level, sc.profile.hasGrades)
		plan.Status = domain.CurriculumDraft
		if name = strings.TrimSpace(name); name != "" {
			plan.Name = name
		}
	}
	if plan.Name == "" {
		plan.Name = "Plan de estudios " + strconv.Itoa(time.Now().Year())
	}
	if fields := validatePlanFields(plan.Name, plan.Notes); len(fields) > 0 {
		return domain.Curriculum{}, domain.Validation(fields)
	}
	created, err := s.repo.CreateCurriculum(ctx, sc.institution.ID, plan)
	if err != nil {
		return domain.Curriculum{}, err
	}
	s.academic.changed(ctx, sc, "curricula")
	return created, nil
}

// fromCurriculumTemplate turns a template into a plan of the level: grade codes become ids (no codes means every
// grade of the level), module codes stay codes for the repository to resolve
func fromCurriculumTemplate(t curriculumTemplate, level domain.Level, hasGrades bool) domain.Curriculum {
	gradeID := map[string]string{}
	var all []string
	for _, g := range level.Grades {
		gradeID[g.Code] = g.ID
		all = append(all, g.ID)
	}
	plan := domain.Curriculum{LevelID: level.ID, Name: t.name, Notes: t.notes}
	for i, m := range t.modules {
		plan.Modules = append(plan.Modules, domain.Module{Code: m.code, Name: m.name, Description: m.description, Position: i})
	}
	for i, st := range t.subjects {
		subject := domain.Subject{Kind: st.kind, Component: st.component, Code: st.code, Name: st.name, Description: st.description, ModuleID: st.module, Position: i}
		if st.hours > 0 {
			h := st.hours
			subject.WeeklyHours = &h
		}
		if st.kind == domain.SubjectUnit || st.kind == domain.SubjectEFSRT {
			theory, practice := st.theory, st.practice
			subject.TheoryCredits, subject.PracticeCredits = &theory, &practice
		}
		if hasGrades {
			if st.grades == nil {
				subject.GradeIDs = all
			} else {
				for _, code := range st.grades {
					if id, ok := gradeID[code]; ok {
						subject.GradeIDs = append(subject.GradeIDs, id)
					}
				}
			}
		}
		for j, c := range st.competencies {
			subject.Competencies = append(subject.Competencies, domain.Competency{Code: c.code, Name: c.name, Position: j})
		}
		plan.Subjects = append(plan.Subjects, subject)
	}
	for i, c := range t.transversal {
		plan.Transversal = append(plan.Transversal, domain.Competency{Code: c.code, Name: c.name, Position: i})
	}
	return plan
}

// Duplicate copies a plan (any status) into a new draft: the way to prepare the next version
func (s *CurriculumService) Duplicate(ctx context.Context, token, slug, id, name string) (domain.Curriculum, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Curriculum{}, err
	}
	source, err := s.repo.Curriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Curriculum{}, err
	}
	copyPlan := source
	copyPlan.Name, copyPlan.Status = strings.TrimSpace(name), domain.CurriculumDraft
	if copyPlan.Name == "" {
		copyPlan.Name = source.Name + " (copia)"
	}
	if fields := validatePlanFields(copyPlan.Name, copyPlan.Notes); len(fields) > 0 {
		return domain.Curriculum{}, domain.Validation(fields)
	}
	// The repository resolves modules by code
	moduleCode := map[string]string{}
	for _, m := range source.Modules {
		moduleCode[m.ID] = m.Code
	}
	copyPlan.Subjects = make([]domain.Subject, len(source.Subjects))
	for i, sub := range source.Subjects {
		sub.ModuleID = moduleCode[sub.ModuleID]
		copyPlan.Subjects[i] = sub
	}
	created, err := s.repo.CreateCurriculum(ctx, sc.institution.ID, copyPlan)
	if err != nil {
		return domain.Curriculum{}, err
	}
	s.academic.changed(ctx, sc, "curricula")
	return created, nil
}

// editable loads the plan and refuses archived ones
func (s *CurriculumService) editable(ctx context.Context, sc academicScope, id string) (domain.Curriculum, error) {
	c, err := s.repo.Curriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Curriculum{}, err
	}
	if c.Status == domain.CurriculumArchived {
		return domain.Curriculum{}, domain.ErrCurriculumArchived
	}
	return c, nil
}

func (s *CurriculumService) Update(ctx context.Context, token, slug, id string, in domain.CurriculumInput) (CurriculumDetail, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return CurriculumDetail{}, err
	}
	if _, err := s.editable(ctx, sc, id); err != nil {
		return CurriculumDetail{}, err
	}
	in.Name, in.Notes = strings.TrimSpace(in.Name), strings.TrimSpace(in.Notes)
	if fields := validatePlanFields(in.Name, in.Notes); len(fields) > 0 {
		return CurriculumDetail{}, domain.Validation(fields)
	}
	if err := s.repo.UpdateCurriculum(ctx, sc.institution.ID, id, in); err != nil {
		return CurriculumDetail{}, err
	}
	s.academic.changed(ctx, sc, "curricula")
	return s.detail(ctx, sc, id)
}

// SetStatus: a draft or archived plan comes into force (the one in force for that level is archived); the one in
// force is archived
func (s *CurriculumService) SetStatus(ctx context.Context, token, slug, id, status string) (CurriculumDetail, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return CurriculumDetail{}, err
	}
	c, err := s.repo.Curriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return CurriculumDetail{}, err
	}
	allowed := map[string][]string{
		domain.CurriculumDraft:    {domain.CurriculumActive},
		domain.CurriculumActive:   {domain.CurriculumArchived},
		domain.CurriculumArchived: {domain.CurriculumActive},
	}
	if !slices.Contains(allowed[c.Status], status) {
		return CurriculumDetail{}, &domain.Error{Kind: domain.KindInvalid, Code: "invalid_status", Message: "Ese cambio de estado no está permitido."}
	}
	if status == domain.CurriculumActive && len(c.Subjects) == 0 {
		return CurriculumDetail{}, &domain.Error{Kind: domain.KindInvalid, Code: "curriculum_empty", Message: "Agrega lo que se enseña antes de poner el plan en vigencia."}
	}
	if err := s.repo.SetCurriculumStatus(ctx, sc.institution.ID, id, status); err != nil {
		return CurriculumDetail{}, err
	}
	s.academic.changed(ctx, sc, "curricula")
	return s.detail(ctx, sc, id)
}

func (s *CurriculumService) Delete(ctx context.Context, token, slug, id string) error {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	c, err := s.repo.Curriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if c.Status != domain.CurriculumDraft {
		return domain.ErrCurriculumNotDraft
	}
	if err := s.repo.DeleteCurriculum(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.academic.changed(ctx, sc, "curricula")
	return nil
}

// ---------- modules (institutes) ----------

func validateModule(in *domain.ModuleInput) error {
	fields := map[string]string{}
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	in.Code = codeFrom(in.Code, in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 3 || n > 160 {
		fields["name"] = "Escribe un nombre de 3 a 160 caracteres."
	}
	if in.Code == "" || len(in.Code) > 40 {
		fields["code"] = "Usa letras, números y guiones (máximo 40)."
	}
	if utf8.RuneCountInString(in.Description) > 1000 {
		fields["description"] = "Máximo 1000 caracteres."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *CurriculumService) moduleScope(ctx context.Context, token, slug string) (academicScope, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return academicScope{}, err
	}
	if !sc.profile.usesFormativeLevels {
		return academicScope{}, &domain.Error{Kind: domain.KindInvalid, Code: "no_modules", Message: "Los módulos formativos son propios de los institutos."}
	}
	return sc, nil
}

func (s *CurriculumService) CreateModule(ctx context.Context, token, slug, curriculumID string, in domain.ModuleInput) (domain.Module, error) {
	sc, err := s.moduleScope(ctx, token, slug)
	if err != nil {
		return domain.Module{}, err
	}
	if _, err := s.editable(ctx, sc, curriculumID); err != nil {
		return domain.Module{}, err
	}
	if err := validateModule(&in); err != nil {
		return domain.Module{}, err
	}
	m, err := s.repo.CreateModule(ctx, sc.institution.ID, curriculumID, in)
	if err == nil {
		s.academic.changed(ctx, sc, "curricula")
	}
	return m, err
}

func (s *CurriculumService) UpdateModule(ctx context.Context, token, slug, id string, in domain.ModuleInput) (domain.Module, error) {
	sc, err := s.moduleScope(ctx, token, slug)
	if err != nil {
		return domain.Module{}, err
	}
	curriculumID, err := s.repo.ModuleCurriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Module{}, err
	}
	if _, err := s.editable(ctx, sc, curriculumID); err != nil {
		return domain.Module{}, err
	}
	if err := validateModule(&in); err != nil {
		return domain.Module{}, err
	}
	m, err := s.repo.UpdateModule(ctx, sc.institution.ID, id, in)
	if err == nil {
		s.academic.changed(ctx, sc, "curricula")
	}
	return m, err
}

// DeleteModule removes the module; its units stay in the plan without a module
func (s *CurriculumService) DeleteModule(ctx context.Context, token, slug, id string) error {
	sc, err := s.moduleScope(ctx, token, slug)
	if err != nil {
		return err
	}
	curriculumID, err := s.repo.ModuleCurriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if _, err := s.editable(ctx, sc, curriculumID); err != nil {
		return err
	}
	if err := s.repo.DeleteModule(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.academic.changed(ctx, sc, "curricula")
	return nil
}

// ---------- subjects ----------

func (s *CurriculumService) validateSubject(sc academicScope, plan domain.Curriculum, level domain.Level, in *domain.SubjectInput) error {
	fields := map[string]string{}
	kind := sc.institution.Kind
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	in.Code = codeFrom(in.Code, in.Name)
	allowed := subjectKindsFor[kind]
	if in.Kind == "" {
		in.Kind = allowed[0]
	}
	if !slices.Contains(allowed, in.Kind) {
		fields["kind"] = "Elige un tipo válido."
	}
	if n := utf8.RuneCountInString(in.Name); n < 2 || n > 160 {
		fields["name"] = "Escribe un nombre de 2 a 160 caracteres."
	}
	if in.Code == "" || len(in.Code) > 40 {
		fields["code"] = "Usa letras, números y guiones (máximo 40)."
	}
	if utf8.RuneCountInString(in.Description) > 1000 {
		fields["description"] = "Máximo 1000 caracteres."
	}
	if in.WeeklyHours != nil && (*in.WeeklyHours < 0 || *in.WeeklyHours > 60) {
		fields["weeklyHours"] = "Las horas semanales van de 0 a 60."
	}
	institute := in.Kind == domain.SubjectUnit || in.Kind == domain.SubjectEFSRT
	if institute {
		switch {
		case in.Kind == domain.SubjectEFSRT:
			in.Component = domain.ComponentEFSRT
		case in.Component != domain.ComponentSpecific && in.Component != domain.ComponentEmployability:
			fields["component"] = "Elige si es específica o de empleabilidad."
		}
		theory, practice := 0, 0
		if in.TheoryCredits != nil {
			theory = *in.TheoryCredits
		}
		if in.PracticeCredits != nil {
			practice = *in.PracticeCredits
		}
		if theory < 0 || practice < 0 || theory > 40 || practice > 40 || theory+practice == 0 {
			fields["theoryCredits"] = "Indica los créditos de teoría y de práctica (al menos uno)."
		}
		in.TheoryCredits, in.PracticeCredits = &theory, &practice
		if in.WeeklyHours == nil {
			// 1 credit = 16 h of theory or 32 h of practice, over 16 weeks
			h := theory + 2*practice
			in.WeeklyHours = &h
		}
		if in.ModuleID != "" && !slices.ContainsFunc(plan.Modules, func(m domain.Module) bool { return m.ID == in.ModuleID }) {
			fields["moduleId"] = "Ese módulo no es de este plan."
		}
		if len(unique(in.GradeIDs)) != 1 {
			fields["gradeIds"] = "Elige el semestre en que se dicta."
		}
	} else {
		in.Component, in.TheoryCredits, in.PracticeCredits, in.ModuleID = "", nil, nil, ""
	}
	in.GradeIDs = unique(in.GradeIDs)
	if sc.profile.hasGrades {
		if len(in.GradeIDs) == 0 {
			fields["gradeIds"] = "Elige al menos un " + strings.ToLower(sc.profile.vocabulary.Grade) + "."
		}
		for _, g := range in.GradeIDs {
			if !slices.ContainsFunc(level.Grades, func(x domain.Grade) bool { return x.ID == g }) {
				fields["gradeIds"] = "Uno de los " + strings.ToLower(sc.profile.vocabulary.Grades) + " no es de " + level.Name + "."
			}
		}
	} else {
		in.GradeIDs = nil
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *CurriculumService) planAndLevel(ctx context.Context, sc academicScope, curriculumID string) (domain.Curriculum, domain.Level, error) {
	plan, err := s.editable(ctx, sc, curriculumID)
	if err != nil {
		return domain.Curriculum{}, domain.Level{}, err
	}
	level, err := s.academic.repo.Level(ctx, sc.institution.ID, plan.LevelID)
	return plan, level, err
}

func (s *CurriculumService) CreateSubject(ctx context.Context, token, slug, curriculumID string, in domain.SubjectInput) (domain.Subject, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Subject{}, err
	}
	plan, level, err := s.planAndLevel(ctx, sc, curriculumID)
	if err != nil {
		return domain.Subject{}, err
	}
	if err := s.validateSubject(sc, plan, level, &in); err != nil {
		return domain.Subject{}, err
	}
	out, err := s.repo.CreateSubject(ctx, sc.institution.ID, curriculumID, in)
	if err == nil {
		s.academic.changed(ctx, sc, "curricula")
	}
	return out, err
}

func (s *CurriculumService) UpdateSubject(ctx context.Context, token, slug, id string, in domain.SubjectInput) (domain.Subject, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Subject{}, err
	}
	curriculumID, err := s.repo.SubjectCurriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Subject{}, err
	}
	plan, level, err := s.planAndLevel(ctx, sc, curriculumID)
	if err != nil {
		return domain.Subject{}, err
	}
	if err := s.validateSubject(sc, plan, level, &in); err != nil {
		return domain.Subject{}, err
	}
	out, err := s.repo.UpdateSubject(ctx, sc.institution.ID, id, in)
	if err == nil {
		s.academic.changed(ctx, sc, "curricula")
	}
	return out, err
}

func (s *CurriculumService) DeleteSubject(ctx context.Context, token, slug, id string) error {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	curriculumID, err := s.repo.SubjectCurriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if _, err := s.editable(ctx, sc, curriculumID); err != nil {
		return err
	}
	if err := s.repo.DeleteSubject(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.academic.changed(ctx, sc, "curricula")
	return nil
}

func (s *CurriculumService) ReorderSubjects(ctx context.Context, token, slug, curriculumID string, ids []string) error {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if _, err := s.editable(ctx, sc, curriculumID); err != nil {
		return err
	}
	if err := s.repo.ReorderSubjects(ctx, sc.institution.ID, curriculumID, ids); err != nil {
		return err
	}
	s.academic.changed(ctx, sc, "curricula")
	return nil
}

// ---------- competencies, capacities and topics ----------

func validateCompetency(sc academicScope, plan domain.Curriculum, in *domain.CompetencyInput) error {
	fields := map[string]string{}
	in.Name, in.Code = strings.TrimSpace(in.Name), strings.TrimSpace(in.Code)
	if n := utf8.RuneCountInString(in.Name); n < 3 || n > 300 {
		fields["name"] = "Escribe de 3 a 300 caracteres."
	}
	if utf8.RuneCountInString(in.Code) > 20 {
		fields["code"] = "Máximo 20 caracteres."
	}
	if in.SubjectID == "" {
		// Only Educación Básica has transversal competencies
		if competencyKindFor[sc.institution.Kind] != domain.CompetencyKind {
			fields["subjectId"] = "Elige a qué pertenece."
		}
	} else if !slices.ContainsFunc(plan.Subjects, func(x domain.Subject) bool { return x.ID == in.SubjectID }) {
		fields["subjectId"] = "No pertenece a este plan."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *CurriculumService) CreateCompetency(ctx context.Context, token, slug, curriculumID string, in domain.CompetencyInput) (domain.Competency, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Competency{}, err
	}
	plan, err := s.editable(ctx, sc, curriculumID)
	if err != nil {
		return domain.Competency{}, err
	}
	if err := validateCompetency(sc, plan, &in); err != nil {
		return domain.Competency{}, err
	}
	out, err := s.repo.CreateCompetency(ctx, sc.institution.ID, curriculumID, competencyKindFor[sc.institution.Kind], in)
	if err == nil {
		s.academic.changed(ctx, sc, "curricula")
	}
	return out, err
}

func (s *CurriculumService) UpdateCompetency(ctx context.Context, token, slug, id string, in domain.CompetencyInput) (domain.Competency, error) {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Competency{}, err
	}
	curriculumID, err := s.repo.CompetencyCurriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.Competency{}, err
	}
	plan, err := s.editable(ctx, sc, curriculumID)
	if err != nil {
		return domain.Competency{}, err
	}
	// It stays where it is: only its code and name change
	for _, sub := range plan.Subjects {
		if slices.ContainsFunc(sub.Competencies, func(c domain.Competency) bool { return c.ID == id }) {
			in.SubjectID = sub.ID
		}
	}
	if err := validateCompetency(sc, plan, &in); err != nil {
		return domain.Competency{}, err
	}
	out, err := s.repo.UpdateCompetency(ctx, sc.institution.ID, id, in)
	if err == nil {
		s.academic.changed(ctx, sc, "curricula")
	}
	return out, err
}

func (s *CurriculumService) DeleteCompetency(ctx context.Context, token, slug, id string) error {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	curriculumID, err := s.repo.CompetencyCurriculum(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if _, err := s.editable(ctx, sc, curriculumID); err != nil {
		return err
	}
	if err := s.repo.DeleteCompetency(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.academic.changed(ctx, sc, "curricula")
	return nil
}

func (s *CurriculumService) ReorderCompetencies(ctx context.Context, token, slug, curriculumID, subjectID string, ids []string) error {
	sc, err := s.academic.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if _, err := s.editable(ctx, sc, curriculumID); err != nil {
		return err
	}
	if err := s.repo.ReorderCompetencies(ctx, sc.institution.ID, curriculumID, subjectID, ids); err != nil {
		return err
	}
	s.academic.changed(ctx, sc, "curricula")
	return nil
}

// ---------- demo data ----------

// SeedDemo gives every level of the Joseph demo institutions its suggested plan, in force, when the level has no
// plan yet. cmd/seed runs it once
func SeedDemo(ctx context.Context, academic port.AcademicRepository, curricula port.CurriculumRepository, slugs []string) (int, error) {
	created := 0
	for _, slug := range slugs {
		inst, err := academic.InstitutionBySlug(ctx, slug)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return created, err
		}
		levels, err := academic.Levels(ctx, inst.ID)
		if err != nil {
			return created, err
		}
		existing, err := curricula.Curricula(ctx, inst.ID)
		if err != nil {
			return created, err
		}
		for _, level := range levels {
			if slices.ContainsFunc(existing, func(c domain.Curriculum) bool { return c.LevelID == level.ID }) {
				continue
			}
			t, ok := curriculumTemplateFor(inst.Kind, level.Code)
			if !ok {
				continue
			}
			plan := fromCurriculumTemplate(t, level, profileFor(inst.Kind).hasGrades)
			plan.Status = domain.CurriculumDraft
			c, err := curricula.CreateCurriculum(ctx, inst.ID, plan)
			if err != nil {
				return created, err
			}
			if err := curricula.SetCurriculumStatus(ctx, inst.ID, c.ID, domain.CurriculumActive); err != nil {
				return created, err
			}
			created++
		}
	}
	return created, nil
}
