package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Participation and permanence (phase E5 of docs/ceba/PLAN.md). The platform records every action of a student as
// an activity event (database triggers); this service reads them: the participation of each student in a classroom
// room, the permanence of the distance sections with the institution's rule (the tutor may adjust a window with a
// reason) and the dropout signals the wellbeing inbox shows

var (
	participationManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "academic_coordinator": true}
	participationViewers  = map[string]bool{"secretary": true, "teacher": true, "tutor": true, "psychologist": true, "program_coordinator": true,
		"special_ed_specialist": true, domain.RoleTechnicalLead: true}
)

type ParticipationService struct {
	repo  port.ParticipationRepository
	auth  *AuthService
	live  *LiveService
	audit *AuditService
	now   func() time.Time
}

func NewParticipationService(repo port.ParticipationRepository, auth *AuthService, live *LiveService, audit *AuditService) *ParticipationService {
	return &ParticipationService{repo: repo, auth: auth, live: live, audit: audit, now: time.Now}
}

type participationScope struct {
	institutionScope
	email string
	name  string
}

func (sc participationScope) manager() bool { return participationManagers[sc.role] }

func (s *ParticipationService) scope(ctx context.Context, token, slug string) (participationScope, error) {
	isc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return participationScope{}, err
	}
	if !participationManagers[isc.role] && !participationViewers[isc.role] {
		return participationScope{}, domain.ErrParticipationForbidden
	}
	sc := participationScope{institutionScope: isc}
	if me, err := s.auth.Principal(ctx, token); err == nil {
		sc.email, sc.name = me.User.Email, me.User.FullName
		if sc.name == "" {
			sc.name = me.User.Email
		}
	}
	return sc, nil
}

// today is the day in Lima at midnight UTC, like the stored dates
func (s *ParticipationService) today() time.Time {
	n := s.now().In(lima())
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ---------- the rule ----------

// PermanenceWindows splits from..to (inclusive days) in windows of the rule and marks each present when the student
// was active on enough of its days; an adjustment of the window decides instead. A window still running counts only
// once it is present, so the current week never lowers the permanence
func PermanenceWindows(rule domain.PermanenceRule, from, to, today time.Time, active []time.Time, adjustments []domain.PermanenceAdjustment) ([]domain.PermanenceWindow, int, int) {
	from, to, today = dayOf(from), dayOf(to), dayOf(today)
	days := map[time.Time]bool{}
	for _, d := range active {
		days[dayOf(d)] = true
	}
	adjusted := map[time.Time]domain.PermanenceAdjustment{}
	for _, a := range adjustments {
		adjusted[dayOf(a.WindowStart)] = a
	}
	out := []domain.PermanenceWindow{}
	present, counted := 0, 0
	for start := from; !start.After(to); start = start.AddDate(0, 0, rule.WindowDays) {
		end := start.AddDate(0, 0, rule.WindowDays-1)
		w := domain.PermanenceWindow{Start: start, End: end}
		for d := start; !d.After(end) && !d.After(to); d = d.AddDate(0, 0, 1) {
			if days[d] {
				w.ActiveDays++
			}
		}
		w.Present = w.ActiveDays >= rule.MinActiveDays
		if a, ok := adjusted[start]; ok {
			w.Present, w.Adjusted, w.Reason = a.Present, true, a.Reason
		}
		running := !end.Before(today)
		if !running || w.Present {
			counted++
			if w.Present {
				present++
			}
		}
		out = append(out, w)
	}
	return out, present, counted
}

// Percent of present windows, 100 when none counted yet
func Percent(present, counted int) int {
	if counted == 0 {
		return 100
	}
	return present * 100 / counted
}

// PermanenceBoard is the rule and the distance sections of a period
type PermanenceBoard struct {
	Rule        domain.PermanenceRule
	PeriodID    string
	Sections    []domain.DistanceSection
	CanEditRule bool
}

func (s *ParticipationService) Board(ctx context.Context, token, slug, periodID string) (PermanenceBoard, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return PermanenceBoard{}, err
	}
	if periodID == "" {
		if periodID, _, _, err = s.repo.RunningPeriod(ctx, sc.institution.ID); err != nil {
			return PermanenceBoard{}, err
		}
	}
	out := PermanenceBoard{PeriodID: periodID, CanEditRule: sc.manager(), Sections: []domain.DistanceSection{}}
	if out.Rule, err = s.repo.Rule(ctx, sc.institution.ID); err != nil {
		return out, err
	}
	if periodID != "" {
		out.Sections, err = s.repo.DistanceSections(ctx, sc.institution.ID, periodID)
	}
	return out, err
}

func (s *ParticipationService) SaveRule(ctx context.Context, token, slug string, r domain.PermanenceRule) (domain.PermanenceRule, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return r, err
	}
	if !sc.manager() {
		return r, domain.ErrPermanenceReadOnly
	}
	fields := map[string]string{}
	if r.WindowDays < 1 || r.WindowDays > 31 {
		fields["windowDays"] = "Entre 1 y 31 días."
	}
	if r.MinActiveDays < 1 || r.MinActiveDays > r.WindowDays {
		fields["minActiveDays"] = "Al menos 1 y no más que los días del periodo de medición."
	}
	if r.InactiveDays < 3 || r.InactiveDays > 90 {
		fields["inactiveDays"] = "Entre 3 y 90 días."
	}
	if r.LowPercent < 1 || r.LowPercent > 100 {
		fields["lowPercent"] = "Entre 1 y 100 %."
	}
	if len(fields) > 0 {
		return r, domain.Validation(fields)
	}
	before, _ := s.repo.Rule(ctx, sc.institution.ID)
	r.UpdatedByName = sc.name
	if err := s.repo.SaveRule(ctx, sc.institution.ID, r); err != nil {
		return r, err
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "attendance", Action: domain.AuditUpdate, Entity: "permanence_rule", EntityID: sc.institution.ID,
		Summary: fmt.Sprintf("Cambió la regla de permanencia: %d día(s) activos cada %d días", r.MinActiveDays, r.WindowDays),
		Changes: map[string]any{"before": before, "after": r}})
	s.changed(ctx, sc)
	return s.repo.Rule(ctx, sc.institution.ID)
}

func (s *ParticipationService) changed(ctx context.Context, sc participationScope) {
	if s.live != nil {
		s.live.Notify(ctx, EventAttendanceChanged, map[string]string{"institution": sc.institution.Slug, "area": "permanence"})
	}
}

// ---------- a section ----------

// StudentPermanence is one student of a distance section: windows, permanence and activity
type StudentPermanence struct {
	Student  domain.PermanenceStudent
	Windows  []domain.PermanenceWindow
	Present  int
	Counted  int
	Percent  int
	Activity domain.StudentActivity
	// Days since the last action (or since the start when none)
	IdleDays int
}

// SectionPermanence is the permanence report of a distance section
type SectionPermanence struct {
	Label      string
	PeriodName string
	Rule       domain.PermanenceRule
	Students   []StudentPermanence
	CanAdjust  bool
	From, To   time.Time
}

// canAdjust: dirección, coordinación and the section's tutor
func (s *ParticipationService) canAdjust(ctx context.Context, sc participationScope, tutorID string) (bool, error) {
	if sc.manager() {
		return true, nil
	}
	if tutorID == "" || sc.email == "" {
		return false, nil
	}
	staff, err := s.repo.StaffIDByEmail(ctx, sc.institution.ID, sc.email)
	return staff != "" && staff == tutorID, err
}

func (s *ParticipationService) Section(ctx context.Context, token, slug, sectionID string) (SectionPermanence, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return SectionPermanence{}, err
	}
	return s.section(ctx, sc, sectionID)
}

func (s *ParticipationService) section(ctx context.Context, sc participationScope, sectionID string) (SectionPermanence, error) {
	periodID, label, tutorID, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return SectionPermanence{}, err
	}
	out := SectionPermanence{Label: label, Students: []StudentPermanence{}}
	var pFrom, pTo time.Time
	if out.PeriodName, pFrom, pTo, err = s.repo.Period(ctx, sc.institution.ID, periodID); err != nil {
		return out, err
	}
	if out.Rule, err = s.repo.Rule(ctx, sc.institution.ID); err != nil {
		return out, err
	}
	if out.CanAdjust, err = s.canAdjust(ctx, sc, tutorID); err != nil {
		return out, err
	}
	students, err := s.repo.SectionStudents(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return out, err
	}
	today := s.today()
	out.From, out.To = dayOf(pFrom), minTime(dayOf(pTo), today)
	ids, enrollments := []string{}, []string{}
	for _, st := range students {
		ids, enrollments = append(ids, st.StudentID), append(enrollments, st.EnrollmentID)
	}
	activity, err := s.repo.Activity(ctx, ids, out.From, out.To)
	if err != nil {
		return out, err
	}
	adjustments, err := s.repo.Adjustments(ctx, enrollments)
	if err != nil {
		return out, err
	}
	for _, st := range students {
		from := maxTime(out.From, dayOf(st.EnrolledOn))
		to := out.To
		if st.EndedOn != nil {
			to = minTime(to, dayOf(*st.EndedOn))
		}
		var act domain.StudentActivity
		if i := slices.IndexFunc(activity, func(a domain.StudentActivity) bool { return a.StudentID == st.StudentID }); i >= 0 {
			act = activity[i]
		}
		var adj []domain.PermanenceAdjustment
		for _, a := range adjustments {
			if a.EnrollmentID == st.EnrollmentID {
				adj = append(adj, a)
			}
		}
		row := StudentPermanence{Student: st, Activity: act}
		if !to.Before(from) {
			row.Windows, row.Present, row.Counted = PermanenceWindows(out.Rule, from, to, today, act.Days, adj)
		}
		row.Percent = Percent(row.Present, row.Counted)
		since := from
		if act.LastAt != nil {
			since = dayOf(act.LastAt.In(lima()))
		}
		row.IdleDays = max(0, int(today.Sub(since).Hours()/24))
		out.Students = append(out.Students, row)
	}
	return out, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// AdjustInput marks a window present or absent with the reason, or removes the adjustment (Clear)
type AdjustInput struct {
	EnrollmentID string
	WindowStart  time.Time
	Present      bool
	Clear        bool
	Reason       string
}

// Adjust is the tutor's correction of a window (a student who worked offline, a call that did not count…); it needs
// a reason and goes to the audit
func (s *ParticipationService) Adjust(ctx context.Context, token, slug string, in AdjustInput) error {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return err
	}
	sectionID, name, err := s.repo.EnrollmentSection(ctx, sc.institution.ID, in.EnrollmentID)
	if err != nil {
		return err
	}
	if sectionID == "" {
		return domain.ErrNotDistanceSection
	}
	_, _, tutorID, err := s.repo.Section(ctx, sc.institution.ID, sectionID)
	if err != nil {
		return err
	}
	ok, err := s.canAdjust(ctx, sc, tutorID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrPermanenceReadOnly
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if !in.Clear && (in.Reason == "" || utf8.RuneCountInString(in.Reason) > 300) {
		return domain.Validation(map[string]string{"reason": "Escribe la justificación (hasta 300 caracteres)."})
	}
	if in.WindowStart.IsZero() || in.WindowStart.After(s.today()) {
		return domain.Validation(map[string]string{"windowStart": "Elige una semana que ya empezó."})
	}
	if err := s.repo.SaveAdjustment(ctx, sc.institution.ID, domain.PermanenceAdjustment{EnrollmentID: in.EnrollmentID, WindowStart: in.WindowStart, Present: in.Present,
		Reason: in.Reason, ByName: sc.name}, in.Clear); err != nil {
		return err
	}
	what := "presente"
	if !in.Present {
		what = "ausente"
	}
	summary := "Ajustó la permanencia de " + name + " (semana del " + in.WindowStart.Format("02/01") + "): " + what
	if in.Clear {
		summary = "Quitó el ajuste de permanencia de " + name + " (semana del " + in.WindowStart.Format("02/01") + ")"
	}
	s.audit.Record(ctx, sc.institutionScope, AuditEntry{Module: "attendance", Action: domain.AuditUpdate, Entity: "permanence_adjustment", EntityID: in.EnrollmentID,
		Summary: summary, Changes: map[string]any{"reason": in.Reason}})
	s.changed(ctx, sc)
	return nil
}

// ---------- a classroom room ----------

// RoomParticipation is the participation of each student of a room's grade in it
func (s *ParticipationService) RoomParticipation(ctx context.Context, token, slug, roomID string) ([]domain.RoomParticipation, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	return s.repo.RoomParticipation(ctx, sc.institution.ID, roomID)
}

// ---------- dropout alerts ----------

// DropoutAlert is a reason to call a distance student
type DropoutAlert struct {
	Signal domain.DropoutSignal
	// inactive | overdue | low_permanence
	Kind    string
	Level   string
	Title   string
	Detail  string
	Percent int
}

// DropoutAlerts reads the running period's distance students: no activity in the rule's days, work past its
// deadline, permanence under the rule's floor. For the wellbeing inbox (the caller already checked who may see it)
func (s *ParticipationService) DropoutAlerts(ctx context.Context, institutionID string) ([]DropoutAlert, error) {
	periodID, pFrom, _, err := s.repo.RunningPeriod(ctx, institutionID)
	if err != nil || periodID == "" {
		return nil, err
	}
	rule, err := s.repo.Rule(ctx, institutionID)
	if err != nil {
		return nil, err
	}
	signals, err := s.repo.DropoutSignals(ctx, institutionID, periodID, s.now())
	if err != nil || len(signals) == 0 {
		return nil, err
	}
	today := s.today()
	ids, enrollments := []string{}, []string{}
	for _, x := range signals {
		ids, enrollments = append(ids, x.StudentID), append(enrollments, x.EnrollmentID)
	}
	activity, err := s.repo.Activity(ctx, ids, dayOf(pFrom), today)
	if err != nil {
		return nil, err
	}
	adjustments, err := s.repo.Adjustments(ctx, enrollments)
	if err != nil {
		return nil, err
	}
	out := []DropoutAlert{}
	for _, x := range signals {
		from := maxTime(dayOf(pFrom), dayOf(x.EnrolledOn))
		since := from
		if x.LastAt != nil {
			since = dayOf(x.LastAt.In(lima()))
		}
		idle := int(today.Sub(since).Hours() / 24)
		inactive := idle >= rule.InactiveDays
		if inactive {
			level := "medium"
			if idle >= 2*rule.InactiveDays {
				level = "high"
			}
			detail := fmt.Sprintf("%d días sin conectarse", idle)
			if x.LastAt == nil {
				detail = fmt.Sprintf("Sin actividad desde que se matriculó (%d días)", idle)
			}
			out = append(out, DropoutAlert{Signal: x, Kind: "inactive", Level: level, Title: "Sin actividad en el aula virtual", Detail: detail})
		}
		if x.Overdue > 0 {
			level := "medium"
			if x.Overdue >= 3 {
				level = "high"
			}
			out = append(out, DropoutAlert{Signal: x, Kind: "overdue", Level: level, Title: "Entregas vencidas", Detail: fmt.Sprintf("%d sin entregar", x.Overdue)})
		}
		var act domain.StudentActivity
		if i := slices.IndexFunc(activity, func(a domain.StudentActivity) bool { return a.StudentID == x.StudentID }); i >= 0 {
			act = activity[i]
		}
		var adj []domain.PermanenceAdjustment
		for _, a := range adjustments {
			if a.EnrollmentID == x.EnrollmentID {
				adj = append(adj, a)
			}
		}
		_, present, counted := PermanenceWindows(rule, from, today, today, act.Days, adj)
		// Two finished windows at least before judging it
		// An inactive student already has the stronger alert
		if p := Percent(present, counted); !inactive && counted >= 2 && p < rule.LowPercent {
			out = append(out, DropoutAlert{Signal: x, Kind: "low_permanence", Level: "medium", Title: "Permanencia baja", Percent: p,
				Detail: fmt.Sprintf("%d %% (mínimo %d %%)", p, rule.LowPercent)})
		}
	}
	return out, nil
}
