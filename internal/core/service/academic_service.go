package service

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"api/internal/core/domain"
	"api/internal/core/port"
)

const (
	EventAcademicChanged = "academic.changed" // {institution, area}: structure, periods or sections changed

	maxSectionBatch = 120
	maxTerms        = 6
)

var (
	// Roles that change the structure; the rest of the staff only reads it
	academicManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	academicViewers  = map[string]bool{"program_coordinator": true, "special_ed_specialist": true, "secretary": true, "teacher": true, "tutor": true, "assistant": true, "psychologist": true, "treasurer": true}

	validShifts          = map[string]bool{"morning": true, "afternoon": true, "evening": true}
	validModes           = map[string]bool{"in_person": true, "blended": true, "distance": true}
	validSeasons         = map[string]bool{"summer": true, "annual": true, "semester": true, "intensive": true, "review": true, "school": true, "other": true}
	validFormativeLevels = map[string]bool{"technical": true, "professional_technical": true, "professional": true}
	termsPerScheme       = map[string]int{domain.TermSchemeBimester: 4, domain.TermSchemeTrimester: 3, domain.TermSchemeSemester: 2, domain.TermSchemeNone: 0}
	termNoun             = map[string]string{domain.TermSchemeBimester: "bimestre", domain.TermSchemeTrimester: "trimestre", domain.TermSchemeSemester: "semestre"}
)

// AcademicService runs the academic structure of each institution: its profile, levels and grades, periods with
// their terms, and the sections of every period (phase 1 of docs/plan-academico.md)
type AcademicService struct {
	repo   port.AcademicRepository
	auth   *AuthService
	live   *LiveService
	logger zerolog.Logger
	now    func() time.Time
}

func NewAcademicService(repo port.AcademicRepository, auth *AuthService, live *LiveService, logger zerolog.Logger) *AcademicService {
	return &AcademicService{repo: repo, auth: auth, live: live, logger: logger, now: time.Now}
}

// AcademicContext is what every academic call answers with besides its data: the institution, its rules and names,
// and whether the person may change things
type AcademicContext struct {
	Institution         domain.Institution
	Vocabulary          AcademicVocabulary
	PeriodKind          string
	TermSchemes         []string
	HasGrades           bool
	UsesAttentionModes  bool
	UsesSeasons         bool
	UsesFormativeLevels bool
	SingleActivePeriod  bool
	CanManage           bool
	Settings            domain.AcademicSettings
}

type academicScope struct {
	institutionScope
	profile academicProfile
}

func (s *AcademicService) scope(ctx context.Context, token, slug string, write bool) (academicScope, error) {
	sc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return academicScope{}, err
	}
	switch {
	case academicManagers[sc.role]:
	case academicViewers[sc.role]:
		if write {
			return academicScope{}, domain.ErrAcademicReadOnly
		}
	default:
		return academicScope{}, domain.ErrAcademicForbidden
	}
	return academicScope{institutionScope: sc, profile: profileFor(sc.institution.Kind)}, nil
}

func (s *AcademicService) contextFor(ctx context.Context, sc academicScope) (AcademicContext, error) {
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return AcademicContext{}, err
	}
	p := sc.profile
	return AcademicContext{
		Institution: sc.institution, Vocabulary: p.vocabulary, PeriodKind: p.periodKind, TermSchemes: p.termSchemes,
		HasGrades: p.hasGrades, UsesAttentionModes: p.usesAttentionModes, UsesSeasons: p.usesSeasons,
		UsesFormativeLevels: p.usesFormativeLevels, SingleActivePeriod: p.singleActivePeriod,
		CanManage: academicManagers[sc.role], Settings: settings,
	}, nil
}

// settings returns the institution's profile, built from its kind the first time it is read
func (s *AcademicService) settings(ctx context.Context, sc academicScope) (domain.AcademicSettings, error) {
	current, err := s.repo.Settings(ctx, sc.institution.ID)
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.AcademicSettings{}, err
	}
	p := sc.profile
	defaults := domain.AcademicSettings{TermScheme: p.termSchemes[0], GradingScale: p.scale, PassingGrade: p.passing, ConductGrade: p.conduct, AttentionModes: []string{}, Shifts: p.defaultShifts, WeekDays: p.weekDays,
		LateAfterMinutes: 10, AbsenceLimitPercent: 30}
	if p.usesAttentionModes {
		defaults.AttentionModes = allModes
	}
	return s.repo.SaveSettings(ctx, sc.institution.ID, defaults)
}

func (s *AcademicService) changed(ctx context.Context, sc academicScope, area string) {
	s.live.Notify(ctx, EventAcademicChanged, map[string]string{"institution": sc.institution.Slug, "area": area})
}

// ---------- Profile ----------

func (s *AcademicService) Context(ctx context.Context, token, slug string) (AcademicContext, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return AcademicContext{}, err
	}
	return s.contextFor(ctx, sc)
}

// UpdateSettings changes the profile; the grading scale is fixed by the kind except the passing mark and conduct
func (s *AcademicService) UpdateSettings(ctx context.Context, token, slug string, in domain.AcademicSettings) (AcademicContext, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return AcademicContext{}, err
	}
	current, err := s.settings(ctx, sc)
	if err != nil {
		return AcademicContext{}, err
	}
	p := sc.profile
	fields := map[string]string{}
	if !slices.Contains(p.termSchemes, in.TermScheme) {
		fields["termScheme"] = "Elige una división de periodos válida."
	}
	next := current
	next.TermScheme = in.TermScheme
	if p.scale == domain.ScaleVigesimal {
		if in.PassingGrade == nil || *in.PassingGrade < 1 || *in.PassingGrade > 20 {
			fields["passingGrade"] = "La nota mínima aprobatoria va de 1 a 20."
		}
		next.PassingGrade = in.PassingGrade
	}
	if p.scale == domain.ScaleLiteral {
		next.ConductGrade = in.ConductGrade
	}
	next.Shifts = unique(in.Shifts)
	if len(next.Shifts) == 0 || !allIn(next.Shifts, validShifts) {
		fields["shifts"] = "Elige al menos un turno."
	}
	if len(in.WeekDays) > 0 {
		days := []int{}
		for _, d := range in.WeekDays {
			if d < 1 || d > 7 {
				fields["weekDays"] = "Elige días de la semana válidos."
			} else if !slices.Contains(days, d) {
				days = append(days, d)
			}
		}
		slices.Sort(days)
		next.WeekDays = days
	}
	// Zero means "keep" (older clients do not send them)
	if in.LateAfterMinutes != 0 || in.AbsenceLimitPercent != 0 {
		if in.LateAfterMinutes < 0 || in.LateAfterMinutes > 120 {
			fields["lateAfterMinutes"] = "La tolerancia va de 0 a 120 minutos."
		}
		if in.AbsenceLimitPercent < 1 || in.AbsenceLimitPercent > 100 {
			fields["absenceLimitPercent"] = "El límite de inasistencias va de 1 a 100 %."
		}
		next.LateAfterMinutes, next.AbsenceLimitPercent = in.LateAfterMinutes, in.AbsenceLimitPercent
	}
	if p.usesAttentionModes {
		next.AttentionModes = unique(in.AttentionModes)
		if len(next.AttentionModes) == 0 || !allIn(next.AttentionModes, validModes) {
			fields["attentionModes"] = "Elige al menos una forma de atención."
		}
	}
	if len(fields) > 0 {
		return AcademicContext{}, domain.Validation(fields)
	}
	if _, err := s.repo.SaveSettings(ctx, sc.institution.ID, next); err != nil {
		return AcademicContext{}, err
	}
	s.changed(ctx, sc, "settings")
	return s.contextFor(ctx, sc)
}

// ---------- Levels and grades ----------

func (s *AcademicService) Structure(ctx context.Context, token, slug string) (AcademicContext, []domain.Level, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return AcademicContext{}, nil, err
	}
	actx, err := s.contextFor(ctx, sc)
	if err != nil {
		return AcademicContext{}, nil, err
	}
	levels, err := s.repo.Levels(ctx, sc.institution.ID)
	return actx, levels, err
}

func (s *AcademicService) validateLevel(sc academicScope, settings domain.AcademicSettings, in *domain.LevelInput) error {
	fields := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Code = codeFrom(in.Code, in.Name)
	switch n := utf8.RuneCountInString(in.Name); {
	case n < 2:
		fields["name"] = "Escribe el nombre."
	case n > 120:
		fields["name"] = "Máximo 120 caracteres."
	}
	if utf8.RuneCountInString(in.Description) > 500 {
		fields["description"] = "Máximo 500 caracteres."
	}
	if in.Code == "" || len(in.Code) > 40 {
		fields["code"] = "Usa letras, números y guiones (máximo 40)."
	}
	p := sc.profile
	if p.usesFormativeLevels {
		if !validFormativeLevels[in.FormativeLevel] {
			fields["formativeLevel"] = "Elige el nivel formativo."
		}
		if in.Credits == nil || *in.Credits < 1 || *in.Credits > 400 {
			fields["credits"] = "Indica los créditos del plan (1 a 400)."
		}
	} else {
		in.FormativeLevel, in.Credits = "", nil
	}
	if p.usesAttentionModes {
		in.AttentionModes = unique(in.AttentionModes)
		if len(in.AttentionModes) == 0 {
			fields["attentionModes"] = "Elige al menos una forma de atención."
		}
		for _, m := range in.AttentionModes {
			if !slices.Contains(settings.AttentionModes, m) {
				fields["attentionModes"] = "La institución no ofrece una de esas formas de atención."
			}
		}
	} else {
		in.AttentionModes = []string{}
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *AcademicService) CreateLevel(ctx context.Context, token, slug string, in domain.LevelInput) (domain.Level, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Level{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return domain.Level{}, err
	}
	in.IsActive = true
	if err := s.validateLevel(sc, settings, &in); err != nil {
		return domain.Level{}, err
	}
	l, err := s.repo.CreateLevel(ctx, sc.institution.ID, in)
	if err != nil {
		return domain.Level{}, err
	}
	s.changed(ctx, sc, "structure")
	return l, nil
}

func (s *AcademicService) UpdateLevel(ctx context.Context, token, slug, levelID string, in domain.LevelInput) (domain.Level, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Level{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return domain.Level{}, err
	}
	if err := s.validateLevel(sc, settings, &in); err != nil {
		return domain.Level{}, err
	}
	l, err := s.repo.UpdateLevel(ctx, sc.institution.ID, levelID, in)
	if err != nil {
		return domain.Level{}, err
	}
	s.changed(ctx, sc, "structure")
	return l, nil
}

func (s *AcademicService) DeleteLevel(ctx context.Context, token, slug, levelID string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteLevel(ctx, sc.institution.ID, levelID); err != nil {
		return err
	}
	s.changed(ctx, sc, "structure")
	return nil
}

func (s *AcademicService) ReorderLevels(ctx context.Context, token, slug string, ids []string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	if err := s.repo.ReorderLevels(ctx, sc.institution.ID, ids); err != nil {
		return err
	}
	s.changed(ctx, sc, "structure")
	return nil
}

func validateGrade(in *domain.GradeInput) error {
	fields := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.ShortName = strings.TrimSpace(in.ShortName)
	in.Code = codeFrom(in.Code, in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 80 {
		fields["name"] = "Escribe el nombre (máximo 80 caracteres)."
	}
	if in.ShortName == "" {
		in.ShortName = in.Name
	}
	if utf8.RuneCountInString(in.ShortName) > 20 {
		fields["shortName"] = "Máximo 20 caracteres."
	}
	if in.Code == "" || len(in.Code) > 40 {
		fields["code"] = "Usa letras, números y guiones (máximo 40)."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

func (s *AcademicService) gradeScope(ctx context.Context, token, slug string) (academicScope, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return academicScope{}, err
	}
	if !sc.profile.hasGrades {
		return academicScope{}, &domain.Error{Kind: domain.KindInvalid, Code: "no_grades", Message: "Este tipo de institución no organiza sus áreas en grados."}
	}
	return sc, nil
}

func (s *AcademicService) CreateGrade(ctx context.Context, token, slug, levelID string, in domain.GradeInput) (domain.Grade, error) {
	sc, err := s.gradeScope(ctx, token, slug)
	if err != nil {
		return domain.Grade{}, err
	}
	if err := validateGrade(&in); err != nil {
		return domain.Grade{}, err
	}
	g, err := s.repo.CreateGrade(ctx, sc.institution.ID, levelID, in)
	if err != nil {
		return domain.Grade{}, err
	}
	s.changed(ctx, sc, "structure")
	return g, nil
}

func (s *AcademicService) UpdateGrade(ctx context.Context, token, slug, gradeID string, in domain.GradeInput) (domain.Grade, error) {
	sc, err := s.gradeScope(ctx, token, slug)
	if err != nil {
		return domain.Grade{}, err
	}
	if err := validateGrade(&in); err != nil {
		return domain.Grade{}, err
	}
	g, err := s.repo.UpdateGrade(ctx, sc.institution.ID, gradeID, in)
	if err != nil {
		return domain.Grade{}, err
	}
	s.changed(ctx, sc, "structure")
	return g, nil
}

func (s *AcademicService) DeleteGrade(ctx context.Context, token, slug, gradeID string) error {
	sc, err := s.gradeScope(ctx, token, slug)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteGrade(ctx, sc.institution.ID, gradeID); err != nil {
		return err
	}
	s.changed(ctx, sc, "structure")
	return nil
}

func (s *AcademicService) ReorderGrades(ctx context.Context, token, slug, levelID string, ids []string) error {
	sc, err := s.gradeScope(ctx, token, slug)
	if err != nil {
		return err
	}
	if err := s.repo.ReorderGrades(ctx, sc.institution.ID, levelID, ids); err != nil {
		return err
	}
	s.changed(ctx, sc, "structure")
	return nil
}

// ApplyTemplate loads the usual structure of the institution kind (Minedu levels and grades, CEPRE areas, IEST
// programs) into an institution that has none yet
func (s *AcademicService) ApplyTemplate(ctx context.Context, token, slug string) ([]domain.Level, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return nil, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, domain.ErrStructureNotEmpty
	}
	template := make([]domain.Level, len(sc.profile.template))
	for i, l := range sc.profile.template {
		l.Position = i
		// Only the forms of attention the institution offers
		if sc.profile.usesAttentionModes {
			var modes []string
			for _, m := range l.AttentionModes {
				if slices.Contains(settings.AttentionModes, m) {
					modes = append(modes, m)
				}
			}
			l.AttentionModes = modes
		}
		grades := make([]domain.Grade, len(l.Grades))
		for j, g := range l.Grades {
			g.Position = j
			grades[j] = g
		}
		l.Grades = grades
		template[i] = l
	}
	if err := s.repo.CreateStructure(ctx, sc.institution.ID, template); err != nil {
		return nil, err
	}
	s.changed(ctx, sc, "structure")
	return s.repo.Levels(ctx, sc.institution.ID)
}

// ---------- Periods and terms ----------

func (s *AcademicService) Periods(ctx context.Context, token, slug string) (AcademicContext, []domain.Period, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return AcademicContext{}, nil, err
	}
	actx, err := s.contextFor(ctx, sc)
	if err != nil {
		return AcademicContext{}, nil, err
	}
	periods, err := s.repo.Periods(ctx, sc.institution.ID)
	return actx, periods, err
}

// PeriodDetail is a period with its terms and sections, plus the structure to add sections from
type PeriodDetail struct {
	Context  AcademicContext
	Period   domain.Period
	Sections []domain.Section
	Levels   []domain.Level
}

func (s *AcademicService) Period(ctx context.Context, token, slug, periodID string) (PeriodDetail, error) {
	sc, err := s.scope(ctx, token, slug, false)
	if err != nil {
		return PeriodDetail{}, err
	}
	actx, err := s.contextFor(ctx, sc)
	if err != nil {
		return PeriodDetail{}, err
	}
	p, err := s.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return PeriodDetail{}, err
	}
	sections, err := s.repo.Sections(ctx, sc.institution.ID, periodID)
	if err != nil {
		return PeriodDetail{}, err
	}
	levels, err := s.repo.Levels(ctx, sc.institution.ID)
	if err != nil {
		return PeriodDetail{}, err
	}
	return PeriodDetail{Context: actx, Period: p, Sections: sections, Levels: levels}, nil
}

func (s *AcademicService) validatePeriod(sc academicScope, in *domain.PeriodInput) error {
	fields := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 3 || n > 80 {
		fields["name"] = "Escribe un nombre de 3 a 80 caracteres."
	}
	if in.StartsOn.IsZero() {
		fields["startsOn"] = "Indica la fecha de inicio."
	}
	if in.EndsOn.IsZero() {
		fields["endsOn"] = "Indica la fecha de término."
	}
	if !in.StartsOn.IsZero() && !in.EndsOn.IsZero() {
		days := int(in.EndsOn.Sub(in.StartsOn).Hours() / 24)
		switch {
		case days <= 0:
			fields["endsOn"] = "Debe terminar después de empezar."
		case days > 400:
			fields["endsOn"] = "Un periodo dura como máximo 400 días."
		case days < termsPerScheme[in.TermScheme]*7:
			fields["endsOn"] = "El periodo es muy corto para esa división."
		}
	}
	if !slices.Contains(sc.profile.termSchemes, in.TermScheme) {
		fields["termScheme"] = "Elige una división válida."
	}
	if sc.profile.usesSeasons {
		if !validSeasons[in.Season] {
			fields["season"] = "Elige la temporada del ciclo."
		}
	} else {
		in.Season = ""
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	return nil
}

// splitTerms divides the period into equal back-to-back terms; the panel adjusts the dates afterwards (vacations)
func splitTerms(scheme string, from, to time.Time) []domain.Term {
	n := termsPerScheme[scheme]
	if n == 0 {
		return []domain.Term{}
	}
	days := int(to.Sub(from).Hours()/24) + 1
	terms := make([]domain.Term, n)
	start := from
	for i := range n {
		end := from.AddDate(0, 0, days*(i+1)/n-1)
		if i == n-1 {
			end = to
		}
		terms[i] = domain.Term{Number: i + 1, Name: roman(i+1) + " " + termNoun[scheme], StartsOn: start, EndsOn: end, Status: domain.TermOpen}
		start = end.AddDate(0, 0, 1)
	}
	return terms
}

func (s *AcademicService) CreatePeriod(ctx context.Context, token, slug string, in domain.PeriodInput) (domain.Period, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Period{}, err
	}
	if in.TermScheme == "" {
		settings, err := s.settings(ctx, sc)
		if err != nil {
			return domain.Period{}, err
		}
		in.TermScheme = settings.TermScheme
	}
	if err := s.validatePeriod(sc, &in); err != nil {
		return domain.Period{}, err
	}
	p, err := s.repo.CreatePeriod(ctx, sc.institution.ID, domain.Period{
		Kind: sc.profile.periodKind, Name: in.Name, Season: in.Season, StartsOn: in.StartsOn, EndsOn: in.EndsOn,
		TermScheme: in.TermScheme, Status: domain.PeriodPlanning, Terms: splitTerms(in.TermScheme, in.StartsOn, in.EndsOn),
	})
	if err != nil {
		return domain.Period{}, err
	}
	s.changed(ctx, sc, "periods")
	return p, nil
}

// UpdatePeriod edits name, season and dates; terms are rebuilt when the division or the dates change while the
// period is still in planning. A running period keeps its terms (each is adjusted on its own)
func (s *AcademicService) UpdatePeriod(ctx context.Context, token, slug, periodID string, in domain.PeriodInput) (domain.Period, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Period{}, err
	}
	current, err := s.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return domain.Period{}, err
	}
	if current.Status == domain.PeriodClosed {
		return domain.Period{}, domain.ErrPeriodClosed
	}
	if in.TermScheme == "" {
		in.TermScheme = current.TermScheme
	}
	if err := s.validatePeriod(sc, &in); err != nil {
		return domain.Period{}, err
	}
	var terms []domain.Term
	reshaped := in.TermScheme != current.TermScheme || !in.StartsOn.Equal(current.StartsOn) || !in.EndsOn.Equal(current.EndsOn)
	if reshaped {
		if current.Status != domain.PeriodPlanning {
			// Running: the scheme stays and the terms must still fit inside the new dates
			if in.TermScheme != current.TermScheme {
				return domain.Period{}, domain.Validation(map[string]string{"termScheme": "La división solo se cambia mientras el periodo está en planificación."})
			}
			for _, t := range current.Terms {
				if t.StartsOn.Before(in.StartsOn) || t.EndsOn.After(in.EndsOn) {
					return domain.Period{}, domain.Validation(map[string]string{"startsOn": "Las fechas deben contener las de cada " + strings.ToLower(termLabel(current.TermScheme)) + "."})
				}
			}
		} else {
			terms = splitTerms(in.TermScheme, in.StartsOn, in.EndsOn)
		}
	}
	current.Name, current.Season, current.StartsOn, current.EndsOn, current.TermScheme = in.Name, in.Season, in.StartsOn, in.EndsOn, in.TermScheme
	p, err := s.repo.UpdatePeriod(ctx, sc.institution.ID, current, terms)
	if err != nil {
		return domain.Period{}, err
	}
	s.changed(ctx, sc, "periods")
	return p, nil
}

func termLabel(scheme string) string {
	if n, ok := termNoun[scheme]; ok {
		return n
	}
	return "tramo"
}

// SetPeriodStatus moves a period: planning → active → closed, and back from closed to active to correct it
func (s *AcademicService) SetPeriodStatus(ctx context.Context, token, slug, periodID, status string) (domain.Period, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Period{}, err
	}
	current, err := s.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return domain.Period{}, err
	}
	allowed := map[string][]string{
		domain.PeriodPlanning: {domain.PeriodActive},
		domain.PeriodActive:   {domain.PeriodPlanning, domain.PeriodClosed},
		domain.PeriodClosed:   {domain.PeriodActive},
	}
	if !slices.Contains(allowed[current.Status], status) {
		return domain.Period{}, &domain.Error{Kind: domain.KindInvalid, Code: "invalid_status", Message: "Ese cambio de estado no está permitido."}
	}
	if status == domain.PeriodActive && sc.profile.singleActivePeriod {
		periods, err := s.repo.Periods(ctx, sc.institution.ID)
		if err != nil {
			return domain.Period{}, err
		}
		for _, p := range periods {
			if p.ID != current.ID && p.Status == domain.PeriodActive {
				return domain.Period{}, domain.ErrActivePeriodTaken
			}
		}
	}
	if err := s.repo.SetPeriodStatus(ctx, sc.institution.ID, periodID, status); err != nil {
		return domain.Period{}, err
	}
	s.changed(ctx, sc, "periods")
	return s.repo.Period(ctx, sc.institution.ID, periodID)
}

func (s *AcademicService) DeletePeriod(ctx context.Context, token, slug, periodID string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	current, err := s.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return err
	}
	if current.Status != domain.PeriodPlanning {
		return domain.ErrPeriodInUse
	}
	if err := s.repo.DeletePeriod(ctx, sc.institution.ID, periodID); err != nil {
		return err
	}
	s.changed(ctx, sc, "periods")
	return nil
}

// UpdateTerm adjusts one term: inside the period, after the previous one and before the next one
func (s *AcademicService) UpdateTerm(ctx context.Context, token, slug, periodID, termID string, in domain.TermInput) (domain.Term, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Term{}, err
	}
	p, err := s.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return domain.Term{}, err
	}
	if p.Status == domain.PeriodClosed {
		return domain.Term{}, domain.ErrPeriodClosed
	}
	idx := slices.IndexFunc(p.Terms, func(t domain.Term) bool { return t.ID == termID })
	if idx < 0 {
		return domain.Term{}, domain.ErrNotFound
	}
	fields := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(in.Name); n < 2 || n > 40 {
		fields["name"] = "Escribe un nombre de 2 a 40 caracteres."
	}
	if in.Status != domain.TermOpen && in.Status != domain.TermClosed {
		fields["status"] = "Estado no válido."
	}
	switch {
	case in.StartsOn.IsZero() || in.EndsOn.IsZero():
		fields["startsOn"] = "Indica las fechas."
	case in.EndsOn.Before(in.StartsOn):
		fields["endsOn"] = "Debe terminar después de empezar."
	case in.StartsOn.Before(p.StartsOn) || in.EndsOn.After(p.EndsOn):
		fields["startsOn"] = "Debe quedar dentro de las fechas del periodo."
	case idx > 0 && !in.StartsOn.After(p.Terms[idx-1].EndsOn):
		fields["startsOn"] = "Debe empezar después de que termine el anterior."
	case idx < len(p.Terms)-1 && !in.EndsOn.Before(p.Terms[idx+1].StartsOn):
		fields["endsOn"] = "Debe terminar antes de que empiece el siguiente."
	}
	if len(fields) > 0 {
		return domain.Term{}, domain.Validation(fields)
	}
	t, err := s.repo.UpdateTerm(ctx, sc.institution.ID, termID, in)
	if err != nil {
		return domain.Term{}, err
	}
	s.changed(ctx, sc, "periods")
	return t, nil
}

// ---------- Sections ----------

func (s *AcademicService) openPeriod(ctx context.Context, sc academicScope, periodID string) (domain.Period, error) {
	p, err := s.repo.Period(ctx, sc.institution.ID, periodID)
	if err != nil {
		return domain.Period{}, err
	}
	if p.Status == domain.PeriodClosed {
		return domain.Period{}, domain.ErrPeriodClosed
	}
	return p, nil
}

func (s *AcademicService) validateSection(sc academicScope, settings domain.AcademicSettings, lvl domain.Level, in *domain.SectionInput) map[string]string {
	fields := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.Room = strings.TrimSpace(in.Room)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 40 {
		fields["name"] = "Escribe el nombre (máximo 40 caracteres)."
	}
	if !slices.Contains(settings.Shifts, in.Shift) {
		fields["shift"] = "Elige uno de los turnos de la institución."
	}
	if in.Capacity < 1 || in.Capacity > 300 {
		fields["capacity"] = "Las vacantes van de 1 a 300."
	}
	if utf8.RuneCountInString(in.Room) > 60 {
		fields["room"] = "Máximo 60 caracteres."
	}
	if sc.profile.usesAttentionModes {
		if !slices.Contains(lvl.AttentionModes, in.AttentionMode) {
			fields["attentionMode"] = "Ese " + strings.ToLower(sc.profile.vocabulary.Level) + " no se ofrece en esa forma de atención."
		}
	} else {
		in.AttentionMode = ""
	}
	return fields
}

// CreateSections creates one section per grade and name in one go ("A, B y C para 1.° a 5.°")
func (s *AcademicService) CreateSections(ctx context.Context, token, slug, periodID string, batch domain.SectionBatch) ([]domain.Section, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return nil, err
	}
	if _, err := s.openPeriod(ctx, sc, periodID); err != nil {
		return nil, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return nil, err
	}
	lvl, err := s.repo.Level(ctx, sc.institution.ID, batch.LevelID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.Validation(map[string]string{"levelId": "Elige un " + strings.ToLower(sc.profile.vocabulary.Level) + "."})
		}
		return nil, err
	}
	in := domain.SectionInput{Shift: batch.Shift, AttentionMode: batch.AttentionMode, Capacity: batch.Capacity, Room: batch.Room, Name: "x"}
	fields := s.validateSection(sc, settings, lvl, &in)
	delete(fields, "name")
	var names []string
	for _, n := range batch.Names {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		fields["names"] = "Escribe al menos un nombre de sección."
	}
	for _, n := range names {
		if utf8.RuneCountInString(n) > 40 {
			fields["names"] = "Cada nombre tiene máximo 40 caracteres."
		}
	}
	grades := []string{""}
	if sc.profile.hasGrades {
		grades = unique(batch.GradeIDs)
		if len(grades) == 0 {
			fields["gradeIds"] = "Elige al menos un " + strings.ToLower(sc.profile.vocabulary.Grade) + "."
		}
		for _, g := range grades {
			if !slices.ContainsFunc(lvl.Grades, func(x domain.Grade) bool { return x.ID == g }) {
				fields["gradeIds"] = "Uno de los grados no pertenece a ese " + strings.ToLower(sc.profile.vocabulary.Level) + "."
			}
		}
	}
	if len(grades)*len(names) > maxSectionBatch {
		fields["names"] = "Son demasiadas secciones de una vez (máximo " + strconv.Itoa(maxSectionBatch) + ")."
	}
	if len(fields) > 0 {
		return nil, domain.Validation(fields)
	}
	var sections []domain.Section
	for _, g := range grades {
		for _, n := range names {
			sections = append(sections, domain.Section{LevelID: lvl.ID, GradeID: g, Name: n, Shift: in.Shift, AttentionMode: in.AttentionMode, Capacity: in.Capacity, Room: in.Room})
		}
	}
	created, err := s.repo.CreateSections(ctx, sc.institution.ID, periodID, sections)
	if err != nil {
		return nil, err
	}
	s.changed(ctx, sc, "sections")
	return created, nil
}

func (s *AcademicService) UpdateSection(ctx context.Context, token, slug, sectionID string, in domain.SectionInput) (domain.Section, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return domain.Section{}, err
	}
	current, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return domain.Section{}, err
	}
	if _, err := s.openPeriod(ctx, sc, current.PeriodID); err != nil {
		return domain.Section{}, err
	}
	settings, err := s.settings(ctx, sc)
	if err != nil {
		return domain.Section{}, err
	}
	lvl, err := s.repo.Level(ctx, sc.institution.ID, current.LevelID)
	if err != nil {
		return domain.Section{}, err
	}
	if fields := s.validateSection(sc, settings, lvl, &in); len(fields) > 0 {
		return domain.Section{}, domain.Validation(fields)
	}
	out, err := s.repo.UpdateSection(ctx, sc.institution.ID, sectionID, in)
	if err != nil {
		return domain.Section{}, err
	}
	s.changed(ctx, sc, "sections")
	return out, nil
}

func (s *AcademicService) DeleteSection(ctx context.Context, token, slug, sectionID string) error {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return err
	}
	current, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return err
	}
	if _, err := s.openPeriod(ctx, sc, current.PeriodID); err != nil {
		return err
	}
	if err := s.repo.DeleteSection(ctx, sc.institution.ID, sectionID); err != nil {
		return err
	}
	s.changed(ctx, sc, "sections")
	return nil
}

// CopySections brings the sections of another period into this one (same level, grade, name, shift and capacity),
// skipping the ones it already has. Returns how many were added
func (s *AcademicService) CopySections(ctx context.Context, token, slug, periodID, fromPeriodID string) (int, error) {
	sc, err := s.scope(ctx, token, slug, true)
	if err != nil {
		return 0, err
	}
	if periodID == fromPeriodID {
		return 0, domain.Validation(map[string]string{"fromPeriodId": "Elige otro periodo."})
	}
	if _, err := s.openPeriod(ctx, sc, periodID); err != nil {
		return 0, err
	}
	source, err := s.repo.Sections(ctx, sc.institution.ID, fromPeriodID)
	if err != nil {
		return 0, err
	}
	if _, err := s.repo.Period(ctx, sc.institution.ID, fromPeriodID); err != nil {
		return 0, err
	}
	existing, err := s.repo.Sections(ctx, sc.institution.ID, periodID)
	if err != nil {
		return 0, err
	}
	key := func(x domain.Section) string { return x.LevelID + "|" + x.GradeID + "|" + x.Name }
	have := map[string]bool{}
	for _, x := range existing {
		have[key(x)] = true
	}
	var add []domain.Section
	for _, x := range source {
		if !have[key(x)] {
			add = append(add, x)
		}
	}
	if len(add) == 0 {
		return 0, nil
	}
	if _, err := s.repo.CreateSections(ctx, sc.institution.ID, periodID, add); err != nil {
		return 0, err
	}
	s.changed(ctx, sc, "sections")
	return len(add), nil
}

// ---------- helpers ----------

// codeFrom keeps a given code or derives one from the name: "Ciclo Avanzado" → "ciclo-avanzado"
func codeFrom(code, name string) string {
	source := strings.TrimSpace(code)
	if source == "" {
		source = name
	}
	plain, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), strings.ToLower(source))
	if err != nil {
		plain = strings.ToLower(source)
	}
	var b strings.Builder
	dash := false
	for _, r := range plain {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := b.String()
	// Codes are at most 40 characters: cut at the last whole word that fits
	if len(out) > 40 {
		out = out[:40]
		if i := strings.LastIndexByte(out, '-'); i > 20 {
			out = out[:i]
		}
	}
	return strings.TrimRight(out, "-")
}

func unique(values []string) []string {
	out := []string{}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func allIn(values []string, valid map[string]bool) bool {
	for _, v := range values {
		if !valid[v] {
			return false
		}
	}
	return true
}
