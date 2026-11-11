package service

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// IncidentCategory is a fault of the catalog of DS 021-2026-MINEDU. Crime: it may be a crime and must be reported
// to the police or the prosecutor's office; Violence: it also goes to SíseVe (school violence protocols)
type IncidentCategory struct {
	Code     string
	Label    string
	Severity string
	Crime    bool
	Violence bool
}

var incidentCatalog = []IncidentCategory{
	{Code: "rules", Label: "Incumplimiento de normas de convivencia del aula", Severity: "minor"},
	{Code: "late", Label: "Tardanza injustificada", Severity: "minor"},
	{Code: "presentation", Label: "Presentación personal o uniforme", Severity: "minor"},
	{Code: "disruption", Label: "Interrupción reiterada de la clase", Severity: "minor"},
	{Code: "minor_other", Label: "Otra falta leve", Severity: "minor"},
	{Code: "repeat_minor", Label: "Reincidencia en faltas leves", Severity: "serious"},
	{Code: "devices", Label: "Uso indebido del celular u otros dispositivos", Severity: "serious"},
	{Code: "late_term", Label: "Más de tres tardanzas injustificadas en el bimestre", Severity: "serious"},
	{Code: "aggression", Label: "Agresión física sin lesión", Severity: "serious", Violence: true},
	{Code: "substances", Label: "Posesión o consumo de sustancias prohibidas", Severity: "serious"},
	{Code: "forged_excuse", Label: "Justificativo adulterado", Severity: "serious"},
	{Code: "discrimination", Label: "Discriminación", Severity: "very_serious", Violence: true},
	{Code: "bullying", Label: "Acoso escolar que afecta la integridad", Severity: "very_serious", Violence: true},
	{Code: "sexual", Label: "Actos de connotación sexual o violencia sexual", Severity: "very_serious", Crime: true, Violence: true},
	{Code: "extortion", Label: "Extorsión", Severity: "very_serious", Crime: true, Violence: true},
	{Code: "academic_fraud", Label: "Fraude académico", Severity: "very_serious"},
	{Code: "forgery", Label: "Falsificación de documentos oficiales", Severity: "very_serious", Crime: true},
	{Code: "data_leak", Label: "Difusión no autorizada de datos personales", Severity: "very_serious"},
	{Code: "drug_dealing", Label: "Promoción o comercialización de drogas", Severity: "very_serious", Crime: true},
	{Code: "weapons", Label: "Portación de armas u objetos punzocortantes", Severity: "very_serious", Crime: true},
}

// IncidentCatalog is the list of faults the forms offer
func IncidentCatalog() []IncidentCategory { return incidentCatalog }

func category(code string) (IncidentCategory, bool) {
	i := slices.IndexFunc(incidentCatalog, func(c IncidentCategory) bool { return c.Code == code })
	if i < 0 {
		return IncidentCategory{}, false
	}
	return incidentCatalog[i], true
}

func categoryLabel(code string) string {
	if c, ok := category(code); ok {
		return c.Label
	}
	return code
}

func severityLabel(s string) string {
	switch s {
	case "very_serious":
		return "Falta muy grave"
	case "serious":
		return "Falta grave"
	}
	return "Falta leve"
}

func measureLabel(k string) string {
	switch k {
	case "suspension":
		return "suspensión"
	case "conditional":
		return "matrícula condicional"
	case "expulsion":
		return "expulsión"
	}
	return "medida correctiva"
}

func itoa(n int) string { return strconv.Itoa(n) }

func percentText(p float64) string { return strings.Replace(fmt.Sprintf("%.0f %%", p), ".", ",", 1) }

// addSchoolDays counts n weekdays after d (the norm's deadlines are in días hábiles)
func addSchoolDays(d time.Time, n int) time.Time {
	for n > 0 {
		d = d.AddDate(0, 0, 1)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			n--
		}
	}
	return d
}

// Grounds of each measure (DS 021-2026-MINEDU). Checked ones are verified against the record; the others need a
// written justification
var measureGrounds = map[string][]string{
	"corrective":  {""},
	"suspension":  {"serious_fault", "three_minor"},
	"conditional": {"repeated_minor", "two_suspensions", "conduct", "no_family_support"},
	"expulsion":   {"three_suspensions", "very_serious", "systematic"},
}

// ---------- register ----------

type DisciplineBoard struct {
	Incidents []domain.Incident
	// Suspensions, conditional enrollments and expulsions of the school year (pending reconsiderations first)
	Measures []port.MeasureRow
	Catalog  []IncidentCategory
	// Measures waiting for a reconsideration decision
	Pending   int
	CanReport bool
	CanDecide bool
	Family    bool
	Today     time.Time
}

func (s *WellbeingService) Discipline(ctx context.Context, token, slug string, from, to *time.Time, severity, status string) (DisciplineBoard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return DisciplineBoard{}, err
	}
	if sc.specialist() {
		return DisciplineBoard{}, domain.ErrWellbeingForbidden
	}
	f := domain.IncidentFilter{InstitutionID: sc.institution.ID, From: from, To: to, Severity: severity, Status: status}
	if !sc.seesAll() {
		students, err := s.repo.Students(ctx, sc.filter())
		if err != nil {
			return DisciplineBoard{}, err
		}
		f.StudentIDs = []string{}
		for _, st := range students {
			f.StudentIDs = append(f.StudentIDs, st.ID)
		}
	}
	list, err := s.repo.Incidents(ctx, f)
	if err != nil {
		return DisciplineBoard{}, err
	}
	markMine(sc, list)
	b := DisciplineBoard{Incidents: list, Catalog: incidentCatalog, CanReport: !sc.family(), CanDecide: sc.director(), Family: sc.family(), Today: s.today()}
	if b.Measures, err = s.repo.MeasureRows(ctx, sc.institution.ID, f.StudentIDs, time.Date(b.Today.Year(), 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		return b, err
	}
	for _, m := range b.Measures {
		if m.ReconsiderationOn != nil && m.ResolvedOn == nil {
			b.Pending++
		}
	}
	return b, nil
}

// SaveIncident registers or corrects a fault. Any staff member reports faults of the students they see; only the
// author or dirección correct one, and not after measures were taken
func (s *WellbeingService) SaveIncident(ctx context.Context, token, slug string, x domain.Incident) (domain.Incident, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Incident{}, err
	}
	if sc.family() || sc.specialist() {
		return domain.Incident{}, domain.ErrWellbeingReadOnly
	}
	if x.ID != "" {
		old, err := s.repo.Incident(ctx, sc.institution.ID, x.ID)
		if err != nil {
			return domain.Incident{}, err
		}
		if !sc.office() && old.ReportedBy != sc.userID {
			return domain.Incident{}, domain.ErrWellbeingReadOnly
		}
		x.StudentID = old.StudentID
	}
	if _, err := s.student(ctx, sc, x.StudentID); err != nil {
		return domain.Incident{}, err
	}
	x.Description = strings.TrimSpace(x.Description)
	fields := map[string]string{}
	cat, ok := category(x.Category)
	if !ok {
		fields["category"] = "Elige la falta del catálogo."
	}
	x.Severity = cat.Severity
	if x.Description == "" || tooLong(x.Description, 2000) {
		fields["description"] = "Describe lo ocurrido (hasta 2000 caracteres)."
	}
	today := s.today()
	if x.OccurredOn.IsZero() || calendarDay(x.OccurredOn).After(today) {
		fields["occurredOn"] = "La fecha no puede ser futura."
	}
	if x.FamilyNotifiedOn != nil && (calendarDay(*x.FamilyNotifiedOn).After(today) || calendarDay(*x.FamilyNotifiedOn).Before(calendarDay(x.OccurredOn))) {
		fields["familyNotifiedOn"] = "La comunicación a la familia va entre la fecha de la falta y hoy."
	}
	if len(fields) > 0 {
		return domain.Incident{}, domain.Validation(fields)
	}
	if x.Status != "closed" {
		x.Status = "open"
	}
	x.ReportedBy, x.ReportedName = sc.userID, sc.name()
	saved, err := s.repo.SaveIncident(ctx, sc.institution.ID, x)
	if err == nil {
		saved.Mine = true
		s.changed(ctx, sc, "discipline", saved.StudentID)
	}
	return saved, err
}

func (s *WellbeingService) DeleteIncident(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	x, err := s.repo.Incident(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if !sc.office() && x.ReportedBy != sc.userID {
		return domain.ErrWellbeingReadOnly
	}
	if len(x.Measures) > 0 && !sc.director() {
		return domain.ErrDirectorMeasure
	}
	if err := s.repo.DeleteIncident(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "discipline", x.StudentID)
	return nil
}

// SetIncidentStatus closes or reopens a fault (its author, the tutor or dirección)
func (s *WellbeingService) SetIncidentStatus(ctx context.Context, token, slug, id, status string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	x, err := s.repo.Incident(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	st, err := s.student(ctx, sc, x.StudentID)
	if err != nil {
		return err
	}
	if !sc.office() && x.ReportedBy != sc.userID && !sc.tutors(st) {
		return domain.ErrWellbeingReadOnly
	}
	if x.Severity != "minor" && !sc.director() {
		return domain.ErrDirectorMeasure
	}
	if status != "open" && status != "closed" {
		return domain.Validation(map[string]string{"status": "Estado no válido."})
	}
	x.Status = status
	if _, err := s.repo.SaveIncident(ctx, sc.institution.ID, x); err != nil {
		return err
	}
	s.changed(ctx, sc, "discipline", x.StudentID)
	return nil
}

// ---------- measures ----------

// MeasureCheck is what the record says about a student for each ground: the measures form shows it and the service
// enforces it
type MeasureCheck struct {
	MinorInTerm      int
	MinorInYear      int
	Suspensions      int
	Conditional      bool
	Expelled         bool
	ConductAverage   *float64
	VerySeriousFault bool
}

func (s *WellbeingService) check(ctx context.Context, sc wellScope, studentID string, on time.Time, incident *domain.Incident) (MeasureCheck, error) {
	inst := sc.institution.ID
	var c MeasureCheck
	yearStart := time.Date(on.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(on.Year(), 12, 31, 0, 0, 0, 0, time.UTC)
	termAt := on
	if incident != nil {
		termAt = incident.OccurredOn
		c.VerySeriousFault = incident.Severity == "very_serious"
	}
	_, from, to, err := s.repo.TermOf(ctx, inst, termAt)
	if err != nil {
		return c, err
	}
	if from.IsZero() {
		// Without terms (or outside them) the norm's "bimestre" is read as the last two months
		from, to = termAt.AddDate(0, -2, 0), termAt
	}
	if c.MinorInTerm, err = s.repo.CountIncidents(ctx, inst, studentID, "minor", from, to); err != nil {
		return c, err
	}
	if c.MinorInYear, err = s.repo.CountIncidents(ctx, inst, studentID, "minor", yearStart, yearEnd); err != nil {
		return c, err
	}
	measures, err := s.repo.Measures(ctx, inst, studentID, yearStart)
	if err != nil {
		return c, err
	}
	for _, m := range measures {
		if !m.Counts() {
			continue
		}
		switch m.Kind {
		case "suspension":
			c.Suspensions++
		case "conditional":
			c.Conditional = true
		case "expulsion":
			c.Expelled = true
		}
	}
	periodID, _, err := s.repo.CurrentPeriod(ctx, inst, on)
	if err != nil {
		return c, err
	}
	if periodID != "" {
		marks, err := s.repo.Conduct(ctx, inst, studentID, periodID)
		if err != nil {
			return c, err
		}
		if len(marks) > 0 {
			sum := 0
			for _, m := range marks {
				sum += m.Score
			}
			avg := float64(sum) / float64(len(marks))
			c.ConductAverage = &avg
		}
	}
	return c, nil
}

// MeasureContext is the record the measure form shows before deciding
func (s *WellbeingService) MeasureContext(ctx context.Context, token, slug, studentID, incidentID string) (MeasureCheck, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return MeasureCheck{}, err
	}
	if sc.family() || sc.specialist() {
		return MeasureCheck{}, domain.ErrWellbeingReadOnly
	}
	if _, err := s.student(ctx, sc, studentID); err != nil {
		return MeasureCheck{}, err
	}
	var incident *domain.Incident
	if incidentID != "" {
		x, err := s.repo.Incident(ctx, sc.institution.ID, incidentID)
		if err != nil {
			return MeasureCheck{}, err
		}
		incident = &x
	}
	return s.check(ctx, sc, studentID, s.today(), incident)
}

// AddMeasure applies a measure. Corrective measures for minor faults are the teacher's (the one who reported, the
// tutor or dirección); suspensions (up to three school days), conditional enrollment and expulsion are the
// director's and need one of the grounds of the norm
func (s *WellbeingService) AddMeasure(ctx context.Context, token, slug string, m domain.Measure) (domain.Measure, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Measure{}, err
	}
	if sc.family() || sc.specialist() {
		return domain.Measure{}, domain.ErrWellbeingReadOnly
	}
	var incident *domain.Incident
	if m.IncidentID != "" {
		x, err := s.repo.Incident(ctx, sc.institution.ID, m.IncidentID)
		if err != nil {
			return domain.Measure{}, err
		}
		incident, m.StudentID = &x, x.StudentID
	}
	st, err := s.student(ctx, sc, m.StudentID)
	if err != nil {
		return domain.Measure{}, err
	}
	grounds, ok := measureGrounds[m.Kind]
	if !ok {
		return domain.Measure{}, domain.Validation(map[string]string{"kind": "Elige la medida."})
	}
	m.Detail = strings.TrimSpace(m.Detail)
	fields := map[string]string{}
	if m.Detail == "" || tooLong(m.Detail, 2000) {
		fields["detail"] = "Describe la medida y cómo se acompañará al estudiante (hasta 2000 caracteres)."
	}
	today := s.today()
	if m.StartsOn.IsZero() {
		m.StartsOn = today
	}
	if !slices.Contains(grounds, m.Ground) {
		fields["ground"] = "Elige la causal."
	}
	switch m.Kind {
	case "corrective":
		m.Days = 0
		if incident == nil {
			fields["incidentId"] = "La medida correctiva va sobre una falta registrada."
		}
	case "suspension":
		if m.Days < 1 || m.Days > 3 {
			fields["days"] = "La suspensión es de 1 a 3 días hábiles."
		}
		if incident == nil {
			fields["incidentId"] = "La suspensión va sobre una falta registrada."
		}
	default:
		m.Days = 0
	}
	if m.NotifiedOn != nil && calendarDay(*m.NotifiedOn).After(today) {
		fields["notifiedOn"] = "La comunicación no puede tener fecha futura."
	}
	if len(fields) > 0 {
		return domain.Measure{}, domain.Validation(fields)
	}

	// Who decides
	minorCorrective := m.Kind == "corrective" && incident.Severity == "minor"
	if minorCorrective {
		if !sc.office() && incident.ReportedBy != sc.userID && !sc.tutors(st) {
			return domain.Measure{}, domain.ErrWellbeingReadOnly
		}
	} else if !sc.director() {
		return domain.Measure{}, domain.ErrDirectorMeasure
	}

	// The grounds that can be checked against the record
	c, err := s.check(ctx, sc, st.ID, m.StartsOn, incident)
	if err != nil {
		return domain.Measure{}, err
	}
	if c.Expelled {
		return domain.Measure{}, domain.Validation(map[string]string{"kind": "El estudiante ya tiene una expulsión vigente."})
	}
	ok = true
	switch m.Ground {
	case "serious_fault":
		ok = incident != nil && incident.Severity != "minor"
	case "three_minor":
		ok = c.MinorInTerm >= 3
	case "repeated_minor":
		ok = c.MinorInYear >= 3
	case "two_suspensions":
		ok = c.Suspensions >= 2
	case "conduct":
		ok = c.ConductAverage != nil && *c.ConductAverage < 11
	case "three_suspensions":
		ok = c.Suspensions >= 3
	case "very_serious":
		ok = c.VerySeriousFault
	case "systematic", "no_family_support":
		// A judgement of dirección: it needs a fuller justification
		ok = len([]rune(m.Detail)) >= 40
	}
	if !ok {
		return domain.Measure{}, domain.ErrMeasureGround
	}
	if m.Kind == "conditional" && c.Conditional {
		return domain.Measure{}, domain.Validation(map[string]string{"kind": "El estudiante ya tiene matrícula condicional este año."})
	}
	m.DecidedName = sc.name()
	saved, err := s.repo.AddMeasure(ctx, sc.institution.ID, m)
	if err != nil {
		return domain.Measure{}, err
	}
	s.changed(ctx, sc, "discipline", st.ID)
	return saved, nil
}

// NotifyMeasure records when the family was formally told (it starts the reconsideration term)
func (s *WellbeingService) NotifyMeasure(ctx context.Context, token, slug, id string, on time.Time) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	m, err := s.repo.Measure(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if _, err := s.student(ctx, sc, m.StudentID); err != nil {
		return err
	}
	if m.Kind != "corrective" && !sc.director() {
		return domain.ErrDirectorMeasure
	}
	if m.Kind == "corrective" && sc.family() {
		return domain.ErrWellbeingReadOnly
	}
	on = calendarDay(on)
	if on.IsZero() || on.After(s.today()) || on.Before(calendarDay(m.StartsOn).AddDate(0, 0, -7)) {
		return domain.Validation(map[string]string{"notifiedOn": "Indica una fecha de comunicación válida (no futura)."})
	}
	if err := s.repo.NotifyMeasure(ctx, sc.institution.ID, id, on); err != nil {
		return err
	}
	s.changed(ctx, sc, "discipline", m.StudentID)
	return nil
}

// RequestReconsideration: the family (or dirección, writing down what the family brought on paper) asks within
// three school days of being told
func (s *WellbeingService) RequestReconsideration(ctx context.Context, token, slug, id, reason string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	m, err := s.repo.Measure(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if _, err := s.student(ctx, sc, m.StudentID); err != nil {
		return err
	}
	if !sc.family() && !sc.office() {
		return domain.ErrWellbeingReadOnly
	}
	if m.Kind == "corrective" {
		return domain.Validation(map[string]string{"id": "Las medidas correctivas no tienen reconsideración."})
	}
	if m.ReconsiderationOn != nil {
		return domain.ErrReconsiderationOpen
	}
	if m.NotifiedOn == nil {
		return domain.ErrNotNotified
	}
	today := s.today()
	if today.After(addSchoolDays(calendarDay(*m.NotifiedOn), 3)) {
		return domain.ErrReconsiderationLate
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || tooLong(reason, 2000) {
		return domain.Validation(map[string]string{"reason": "Explica por qué pides la reconsideración (hasta 2000 caracteres)."})
	}
	if err := s.repo.RequestReconsideration(ctx, sc.institution.ID, id, today, reason, sc.name()); err != nil {
		return err
	}
	s.changed(ctx, sc, "discipline", m.StudentID)
	return nil
}

// ResolveReconsideration: dirección (with the Comité de Gestión del Bienestar when it applies) upholds or revokes
func (s *WellbeingService) ResolveReconsideration(ctx context.Context, token, slug, id, outcome, resolution string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	if !sc.director() {
		return domain.ErrDirectorMeasure
	}
	m, err := s.repo.Measure(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if m.ReconsiderationOn == nil {
		return domain.Validation(map[string]string{"id": "Esa medida no tiene pedido de reconsideración."})
	}
	if m.ResolvedOn != nil {
		return domain.Validation(map[string]string{"id": "La reconsideración ya fue resuelta."})
	}
	resolution = strings.TrimSpace(resolution)
	fields := map[string]string{}
	if outcome != "upheld" && outcome != "revoked" {
		fields["outcome"] = "Elige si se confirma o se deja sin efecto."
	}
	if resolution == "" || tooLong(resolution, 2000) {
		fields["resolution"] = "Fundamenta la decisión (hasta 2000 caracteres)."
	}
	if len(fields) > 0 {
		return domain.Validation(fields)
	}
	if err := s.repo.ResolveReconsideration(ctx, sc.institution.ID, id, s.today(), outcome, resolution); err != nil {
		return err
	}
	s.changed(ctx, sc, "discipline", m.StudentID)
	return nil
}

// DeleteMeasure removes a measure entered by mistake (dirección; the teacher their own corrective ones)
func (s *WellbeingService) DeleteMeasure(ctx context.Context, token, slug, id string) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	m, err := s.repo.Measure(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if !sc.director() && (m.Kind != "corrective" || m.DecidedName != sc.name()) {
		return domain.ErrDirectorMeasure
	}
	if err := s.repo.DeleteMeasure(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "discipline", m.StudentID)
	return nil
}

// ---------- special education: POI ----------

type PlansBoard struct {
	Plans       []domain.IndividualPlan
	Students    []domain.WellStudent
	Specialists []domain.Staff
	PeriodID    string
	PeriodName  string
	CanManage   bool
	CanWrite    bool
}

func (s *WellbeingService) ebeScope(ctx context.Context, token, slug string) (wellScope, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, err
	}
	if !sc.ebe() {
		return sc, domain.ErrNotSpecialEd
	}
	if sc.role == "assistant" {
		return sc, domain.ErrWellbeingForbidden
	}
	return sc, nil
}

// planReaders: who may open the POIs of a student (teachers of their sections apply the adaptations)
func (s *WellbeingService) readablePlans(ctx context.Context, sc wellScope, periodID string) ([]domain.IndividualPlan, error) {
	inst := sc.institution.ID
	switch {
	case sc.office() || sc.psychologist():
		return s.repo.Plans(ctx, inst, periodID, "", nil)
	case sc.specialist():
		return s.repo.Plans(ctx, inst, periodID, sc.staffID, nil)
	}
	students, err := s.repo.Students(ctx, sc.filter())
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, st := range students {
		ids = append(ids, st.ID)
	}
	plans, err := s.repo.Plans(ctx, inst, periodID, "", ids)
	if sc.family() {
		plans = slices.DeleteFunc(plans, func(p domain.IndividualPlan) bool { return p.Status != "approved" })
	}
	return plans, err
}

func (s *WellbeingService) Plans(ctx context.Context, token, slug, periodID string) (PlansBoard, error) {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return PlansBoard{}, err
	}
	b := PlansBoard{PeriodID: periodID, CanManage: sc.office(), CanWrite: sc.office() || sc.specialist()}
	if periodID == "" {
		if b.PeriodID, b.PeriodName, err = s.repo.CurrentPeriod(ctx, sc.institution.ID, s.today()); err != nil {
			return b, err
		}
	}
	if b.PeriodID == "" {
		return b, nil
	}
	if b.Plans, err = s.readablePlans(ctx, sc, b.PeriodID); err != nil {
		return b, err
	}
	if sc.office() {
		if b.Students, err = s.repo.Students(ctx, port.WellStudentFilter{InstitutionID: sc.institution.ID}); err != nil {
			return b, err
		}
		if b.Specialists, err = s.repo.Specialists(ctx, sc.institution.ID); err != nil {
			return b, err
		}
	}
	return b, nil
}

func (s *WellbeingService) Plan(ctx context.Context, token, slug, id string) (domain.IndividualPlan, error) {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return domain.IndividualPlan{}, err
	}
	p, err := s.repo.Plan(ctx, sc.institution.ID, id)
	if err != nil {
		return p, err
	}
	readable, err := s.readablePlans(ctx, sc, p.PeriodID)
	if err != nil {
		return p, err
	}
	if !slices.ContainsFunc(readable, func(x domain.IndividualPlan) bool { return x.ID == id }) {
		return domain.IndividualPlan{}, domain.ErrNotYourPlan
	}
	return p, nil
}

// SavePlan: coordinación opens the plan and assigns the specialist; the specialist writes it. An approved plan is
// read-only until coordinación reopens it
func (s *WellbeingService) SavePlan(ctx context.Context, token, slug string, p domain.IndividualPlan) (domain.IndividualPlan, error) {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return domain.IndividualPlan{}, err
	}
	if !sc.office() && !sc.specialist() {
		return domain.IndividualPlan{}, domain.ErrWellbeingReadOnly
	}
	inst := sc.institution.ID
	if p.ID != "" {
		old, err := s.repo.Plan(ctx, inst, p.ID)
		if err != nil {
			return domain.IndividualPlan{}, err
		}
		if sc.specialist() && old.SpecialistID != sc.staffID {
			return domain.IndividualPlan{}, domain.ErrNotYourPlan
		}
		if old.Status == "approved" {
			return domain.IndividualPlan{}, domain.ErrPlanApproved
		}
		p.StudentID, p.PeriodID = old.StudentID, old.PeriodID
		if sc.specialist() {
			p.SpecialistID = old.SpecialistID
		}
	} else if !sc.office() {
		// Only coordinación opens a plan (it decides who leads it)
		return domain.IndividualPlan{}, domain.ErrWellbeingReadOnly
	}
	fields := map[string]string{}
	if p.PeriodID == "" {
		if p.PeriodID, _, err = s.repo.CurrentPeriod(ctx, inst, s.today()); err != nil {
			return domain.IndividualPlan{}, err
		}
	}
	if p.ID == "" {
		list, err := s.repo.Students(ctx, port.WellStudentFilter{InstitutionID: inst, StudentIDs: []string{p.StudentID}})
		if err != nil {
			return domain.IndividualPlan{}, err
		}
		if len(list) == 0 {
			fields["studentId"] = "Elige un estudiante matriculado."
		}
	}
	if p.SpecialistID != "" && sc.office() {
		specialists, err := s.repo.Specialists(ctx, inst)
		if err != nil {
			return domain.IndividualPlan{}, err
		}
		if !slices.ContainsFunc(specialists, func(st domain.Staff) bool { return st.ID == p.SpecialistID }) {
			fields["specialistId"] = "Elige a un especialista o terapeuta del personal."
		}
	}
	texts := []struct {
		v     *string
		field string
		max   int
	}{
		{&p.Needs, "needs", 2000}, {&p.Strengths, "strengths", 2000}, {&p.EducationalResponse, "educationalResponse", 4000},
		{&p.CurricularAdaptations, "curricularAdaptations", 4000}, {&p.AccessAdaptations, "accessAdaptations", 4000},
		{&p.EvaluationCriteria, "evaluationCriteria", 2000}, {&p.Actors, "actors", 2000}, {&p.FamilyCommitments, "familyCommitments", 2000},
	}
	for _, t := range texts {
		*t.v = strings.TrimSpace(*t.v)
		if tooLong(*t.v, t.max) {
			fields[t.field] = fmt.Sprintf("Admite hasta %d caracteres.", t.max)
		}
	}
	if len(fields) > 0 {
		return domain.IndividualPlan{}, domain.Validation(fields)
	}
	saved, err := s.repo.SavePlan(ctx, inst, p)
	if err == nil {
		s.changed(ctx, sc, "plans", saved.StudentID)
	}
	return saved, err
}

// SetPlanStatus: coordinación approves a complete plan (the family can then read it) or reopens it
func (s *WellbeingService) SetPlanStatus(ctx context.Context, token, slug, id, status string) (domain.IndividualPlan, error) {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return domain.IndividualPlan{}, err
	}
	if !sc.office() {
		return domain.IndividualPlan{}, domain.ErrWellbeingReadOnly
	}
	p, err := s.repo.Plan(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.IndividualPlan{}, err
	}
	switch status {
	case "approved":
		missing := map[string]string{}
		if p.Needs == "" {
			missing["needs"] = "Falta describir las necesidades."
		}
		if p.EducationalResponse == "" {
			missing["educationalResponse"] = "Falta la respuesta educativa."
		}
		if p.CurricularAdaptations == "" && p.AccessAdaptations == "" {
			missing["curricularAdaptations"] = "Indica al menos las adaptaciones curriculares o de acceso."
		}
		if p.EvaluationCriteria == "" {
			missing["evaluationCriteria"] = "Faltan los criterios de evaluación."
		}
		if len(missing) > 0 {
			return domain.IndividualPlan{}, domain.Validation(missing)
		}
		t := s.today()
		err = s.repo.SetPlanStatus(ctx, sc.institution.ID, id, "approved", sc.name(), &t)
	case "draft":
		err = s.repo.SetPlanStatus(ctx, sc.institution.ID, id, "draft", "", nil)
	default:
		return domain.IndividualPlan{}, domain.Validation(map[string]string{"status": "Estado no válido."})
	}
	if err != nil {
		return domain.IndividualPlan{}, err
	}
	s.changed(ctx, sc, "plans", p.StudentID)
	return s.repo.Plan(ctx, sc.institution.ID, id)
}

func (s *WellbeingService) DeletePlan(ctx context.Context, token, slug, id string) error {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return err
	}
	if !sc.office() {
		return domain.ErrWellbeingReadOnly
	}
	p, err := s.repo.Plan(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if p.Status == "approved" {
		return domain.ErrPlanApproved
	}
	if err := s.repo.DeletePlan(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "plans", p.StudentID)
	return nil
}

// ---------- special education: therapies ----------

var therapyKinds = map[string]bool{"language": true, "physical": true, "occupational": true, "psychomotor": true, "behavioral": true, "learning": true}

type TherapyLog struct {
	Sessions []domain.TherapySession
	Students []domain.WellStudent
	// Minutes given per student in the range (attended sessions)
	CanWrite bool
	Family   bool
}

func (s *WellbeingService) Therapies(ctx context.Context, token, slug string, from, to *time.Time, studentID string) (TherapyLog, error) {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return TherapyLog{}, err
	}
	f := port.TherapyFilter{InstitutionID: sc.institution.ID, From: from, To: to}
	students, err := s.repo.Students(ctx, sc.filter())
	if err != nil {
		return TherapyLog{}, err
	}
	if !sc.seesAll() {
		f.StudentIDs = []string{}
		for _, st := range students {
			f.StudentIDs = append(f.StudentIDs, st.ID)
		}
	}
	if studentID != "" {
		if f.StudentIDs != nil && !slices.Contains(f.StudentIDs, studentID) {
			return TherapyLog{}, domain.ErrNotYourStudent
		}
		f.StudentIDs = []string{studentID}
	}
	log := TherapyLog{CanWrite: sc.office() || sc.specialist() || sc.psychologist(), Family: sc.family()}
	if log.Sessions, err = s.repo.Therapies(ctx, f); err != nil {
		return log, err
	}
	for i := range log.Sessions {
		log.Sessions[i].Mine = sc.staffID != "" && log.Sessions[i].TherapistID == sc.staffID
		if sc.family() {
			log.Sessions[i].Notes = ""
		}
	}
	if log.CanWrite {
		// The students one can log a session for: the specialist's own, or every special-needs student
		for _, st := range students {
			if st.SpecialNeeds || sc.specialist() {
				log.Students = append(log.Students, st)
			}
		}
	}
	return log, nil
}

// SaveTherapy logs (or corrects) a session: the specialist for their students, psychology and dirección for any
func (s *WellbeingService) SaveTherapy(ctx context.Context, token, slug string, t domain.TherapySession) (domain.TherapySession, error) {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return domain.TherapySession{}, err
	}
	if !sc.office() && !sc.specialist() && !sc.psychologist() {
		return domain.TherapySession{}, domain.ErrWellbeingReadOnly
	}
	inst := sc.institution.ID
	if t.ID != "" {
		old, err := s.repo.Therapy(ctx, inst, t.ID)
		if err != nil {
			return domain.TherapySession{}, err
		}
		if !sc.office() && old.TherapistID != sc.staffID {
			return domain.TherapySession{}, domain.ErrWellbeingReadOnly
		}
		t.StudentID, t.TherapistID, t.TherapistName = old.StudentID, old.TherapistID, old.TherapistName
	} else {
		t.TherapistID, t.TherapistName = sc.staffID, sc.staffName
		if t.TherapistName == "" {
			t.TherapistName = sc.name()
		}
	}
	if _, err := s.student(ctx, sc, t.StudentID); err != nil {
		return domain.TherapySession{}, err
	}
	t.Goal, t.Notes, t.HomeTasks = strings.TrimSpace(t.Goal), strings.TrimSpace(t.Notes), strings.TrimSpace(t.HomeTasks)
	fields := map[string]string{}
	if !therapyKinds[t.Kind] {
		fields["kind"] = "Elige el tipo de terapia."
	}
	if t.HeldOn.IsZero() || calendarDay(t.HeldOn).After(s.today()) {
		fields["heldOn"] = "La fecha no puede ser futura."
	}
	if t.Minutes < 10 || t.Minutes > 240 {
		fields["minutes"] = "La sesión dura entre 10 y 240 minutos."
	}
	if t.Goal == "" || tooLong(t.Goal, 500) {
		fields["goal"] = "Escribe el objetivo de la sesión (hasta 500 caracteres)."
	}
	if tooLong(t.Notes, 2000) {
		fields["notes"] = "Las notas admiten hasta 2000 caracteres."
	}
	if tooLong(t.HomeTasks, 1000) {
		fields["homeTasks"] = "Las tareas para casa admiten hasta 1000 caracteres."
	}
	if len(fields) > 0 {
		return domain.TherapySession{}, domain.Validation(fields)
	}
	saved, err := s.repo.SaveTherapy(ctx, inst, t)
	saved.Mine = saved.TherapistID != "" && saved.TherapistID == sc.staffID
	if err == nil {
		s.changed(ctx, sc, "therapies", saved.StudentID)
	}
	return saved, err
}

func (s *WellbeingService) DeleteTherapy(ctx context.Context, token, slug, id string) error {
	sc, err := s.ebeScope(ctx, token, slug)
	if err != nil {
		return err
	}
	t, err := s.repo.Therapy(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if !sc.office() && (t.TherapistID == "" || t.TherapistID != sc.staffID) {
		return domain.ErrWellbeingReadOnly
	}
	if err := s.repo.DeleteTherapy(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "therapies", t.StudentID)
	return nil
}

// markMine flags the faults the person reported (they may correct them)
func markMine(sc wellScope, list []domain.Incident) {
	for i := range list {
		list[i].Mine = sc.userID != "" && list[i].ReportedBy == sc.userID
	}
}

// InstitutionName heads the printed POI
func (s *WellbeingService) InstitutionName(ctx context.Context, token, slug string) (string, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return "", err
	}
	return sc.institution.Name, nil
}
