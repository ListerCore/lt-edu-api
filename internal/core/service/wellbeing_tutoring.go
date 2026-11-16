package service

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Virtual tutoring (phase E6 of docs/ceba/PLAN.md). The tutor in force of a student is the one assigned to them, or
// else the tutor of their section. The tutor sees their students with what needs attention (dropout alerts of E5,
// work pending, unread messages), schedules individual or group sessions, records what was agreed (an interview in
// each attendee's file), writes to the student inside the platform and refers to psychology or the SAANEE

// EventTutoringChanged {institution, area: assignments | sessions | messages | saanee, thread}
const EventTutoringChanged = "tutoring.changed"

// SetTutoring adds virtual tutoring; set by the server
func (s *WellbeingService) SetTutoring(r port.TutoringRepository) { s.tutoring = r }

func (s *WellbeingService) tutoringChanged(ctx context.Context, sc wellScope, area, thread string) {
	s.attendance.academic.live.Notify(ctx, EventTutoringChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "thread": thread})
}

func (s *WellbeingService) tutorAudit(ctx context.Context, sc wellScope, e AuditEntry) {
	e.Module = "tutoring"
	s.attendance.academic.audit.Record(ctx, sc.institutionScope, e)
}

// tutoringScope is the wellbeing scope of staff (families have their own view)
func (s *WellbeingService) tutoringScope(ctx context.Context, token, slug string) (wellScope, string, error) {
	if s.tutoring == nil {
		return wellScope{}, "", domain.ErrNotFound
	}
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return sc, "", err
	}
	periodID, err := s.tutoring.RunningPeriod(ctx, sc.institution.ID)
	return sc, periodID, err
}

// tutorOf is the student's tutor in force in the running period (StaffID "" when none)
func (s *WellbeingService) tutorOf(ctx context.Context, sc wellScope, periodID, studentID string) (domain.TutorStudent, error) {
	if periodID == "" {
		return domain.TutorStudent{}, domain.ErrNotFound
	}
	all, err := s.tutoring.TutorStudents(ctx, sc.institution.ID, periodID, "")
	if err != nil {
		return domain.TutorStudent{}, err
	}
	i := slices.IndexFunc(all, func(t domain.TutorStudent) bool { return t.StudentID == studentID })
	if i < 0 {
		return domain.TutorStudent{}, domain.ErrNotFound
	}
	return all[i], nil
}

// tutorsStudent: the office, or the student's tutor in force
func (sc wellScope) tutorsStudent(t domain.TutorStudent) bool {
	return sc.office() || (sc.staffID != "" && t.StaffID == sc.staffID)
}

// ---------- board ----------

type TutorBoardRow struct {
	domain.TutorStudent
	// Days since the last action (-1: never)
	IdleDays    int
	Alerts      []string
	Overdue     int
	DueSoon     int
	Unread      int
	NextSession *time.Time
	Saanee      string
}

type TutorBoard struct {
	// The tutor shown ("" for dirección looking at every student)
	StaffID   string
	StaffName string
	// Dirección and coordinación pick the tutor
	Tutors    []domain.TutorLoad
	Rows      []TutorBoardRow
	Alerts    []domain.WellAlert
	Pending   []domain.PendingWork
	Sessions  []domain.TutoringSession
	Threads   []domain.Thread
	CanAssign bool
	// The viewer writes to the students (has a staff record and tutors them)
	CanMessage bool
	Today      time.Time
}

// TutorBoard is the tutor's day: their students with what needs attention, the dropout alerts, the work pending, the
// sessions and the conversations. Dirección and coordinación may look at any tutor's (staffID) or at everyone's
func (s *WellbeingService) TutorBoard(ctx context.Context, token, slug, staffID string) (TutorBoard, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return TutorBoard{}, err
	}
	if sc.family() {
		return TutorBoard{}, domain.ErrWellbeingForbidden
	}
	target := sc.staffID
	if sc.office() {
		target = staffID
	} else if target == "" {
		return TutorBoard{}, domain.ErrNotTutor
	}
	today := s.today()
	b := TutorBoard{StaffID: target, CanAssign: sc.office(), CanMessage: sc.staffID != "", Today: today}
	if periodID == "" {
		return b, nil
	}
	inst := sc.institution.ID
	if b.Tutors, err = s.tutoring.TutorLoads(ctx, inst, periodID); err != nil {
		return b, err
	}
	if i := slices.IndexFunc(b.Tutors, func(t domain.TutorLoad) bool { return t.StaffID == target }); i >= 0 {
		b.StaffName = b.Tutors[i].Name
	}
	students, err := s.tutoring.TutorStudents(ctx, inst, periodID, target)
	if err != nil {
		return b, err
	}
	ids := make([]string, 0, len(students))
	rows := map[string]*TutorBoardRow{}
	b.Rows = make([]TutorBoardRow, len(students))
	for i, st := range students {
		b.Rows[i] = TutorBoardRow{TutorStudent: st, IdleDays: -1}
		if st.LastAt != nil {
			b.Rows[i].IdleDays = int(today.Sub(dayOf(st.LastAt.In(lima()))).Hours() / 24)
		}
		ids = append(ids, st.StudentID)
		rows[st.StudentID] = &b.Rows[i]
	}

	// Dropout alerts of the distance students (E5) and commitments past due
	if s.participation != nil {
		dropouts, err := s.participation.DropoutAlerts(ctx, inst)
		if err != nil {
			return b, err
		}
		for _, d := range dropouts {
			r, ok := rows[d.Signal.StudentID]
			if !ok {
				continue
			}
			r.Alerts = append(r.Alerts, d.Kind)
			b.Alerts = append(b.Alerts, domain.WellAlert{Kind: "dropout", Level: d.Level, StudentID: r.StudentID, StudentName: r.Name, Placement: r.Placement,
				Title: d.Title, Detail: d.Detail, Since: d.Signal.LastAt, Code: d.Kind})
		}
	}
	due, err := s.repo.Commitments(ctx, inst, ids, true)
	if err != nil {
		return b, err
	}
	for _, c := range due {
		if r, ok := rows[c.StudentID]; ok && c.DueOn.Before(today) {
			on := c.DueOn
			r.Alerts = append(r.Alerts, "commitment")
			b.Alerts = append(b.Alerts, domain.WellAlert{Kind: "commitment", Level: "medium", StudentID: c.StudentID, StudentName: r.Name, Placement: r.Placement,
				Title: "Compromiso vencido sin revisar", Detail: c.Description, Since: &on, Code: "commitment"})
		}
	}
	slices.SortStableFunc(b.Alerts, func(a, c domain.WellAlert) int {
		if a.Level != c.Level {
			if a.Level == "high" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.StudentName, c.StudentName)
	})

	// Work not handed in: the last two weeks and the next one
	if len(ids) > 0 {
		if b.Pending, err = s.tutoring.PendingWork(ctx, inst, periodID, ids, today.AddDate(0, 0, -14), today.AddDate(0, 0, 8), s.attendance.now()); err != nil {
			return b, err
		}
	}
	for _, w := range b.Pending {
		if r, ok := rows[w.StudentID]; ok {
			if w.Overdue {
				r.Overdue++
			} else {
				r.DueSoon++
			}
		}
	}

	// Sessions: two weeks back and what is ahead
	from := today.AddDate(0, 0, -14)
	if b.Sessions, err = s.tutoring.Sessions(ctx, domain.SessionFilter{InstitutionID: inst, StaffID: target, From: &from}); err != nil {
		return b, err
	}
	now := s.attendance.now()
	for _, x := range b.Sessions {
		if x.Status != domain.SessionScheduled || x.ScheduledAt.Before(now.Add(-time.Duration(x.DurationMin)*time.Minute)) {
			continue
		}
		for _, a := range x.Attendees {
			if r, ok := rows[a.StudentID]; ok && (r.NextSession == nil || x.ScheduledAt.Before(*r.NextSession)) {
				at := x.ScheduledAt
				r.NextSession = &at
			}
		}
	}

	// Conversations of the viewer
	if sc.staffID != "" {
		if b.Threads, err = s.tutoring.Threads(ctx, inst, sc.staffID, ""); err != nil {
			return b, err
		}
		for _, t := range b.Threads {
			if r, ok := rows[t.StudentID]; ok {
				r.Unread += t.Unread
			}
		}
	}

	// SAANEE referrals still open
	if len(ids) > 0 {
		refs, err := s.tutoring.SaaneeReferrals(ctx, inst, ids)
		if err != nil {
			return b, err
		}
		for _, x := range refs {
			if r, ok := rows[x.StudentID]; ok && x.Status != domain.SaaneeClosed && r.Saanee == "" {
				r.Saanee = x.Status
			}
		}
	}
	return b, nil
}

// ---------- assignments ----------

type TutorAssignments struct {
	Loads      []domain.TutorLoad
	Candidates []domain.TutorCandidate
	Students   []domain.TutorStudent
}

func (s *WellbeingService) TutorAssignments(ctx context.Context, token, slug string) (TutorAssignments, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return TutorAssignments{}, err
	}
	if !sc.office() {
		return TutorAssignments{}, domain.ErrTutorAssignOffice
	}
	out := TutorAssignments{}
	if out.Candidates, err = s.tutoring.TutorCandidates(ctx, sc.institution.ID); err != nil || periodID == "" {
		return out, err
	}
	if out.Loads, err = s.tutoring.TutorLoads(ctx, sc.institution.ID, periodID); err != nil {
		return out, err
	}
	out.Students, err = s.tutoring.TutorStudents(ctx, sc.institution.ID, periodID, "")
	return out, err
}

// AssignTutor gives the students a tutor of their own (staffID "" brings them back to their section's tutor)
func (s *WellbeingService) AssignTutor(ctx context.Context, token, slug string, studentIDs []string, staffID string) (TutorAssignments, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return TutorAssignments{}, err
	}
	if !sc.office() {
		return TutorAssignments{}, domain.ErrTutorAssignOffice
	}
	if len(studentIDs) == 0 || len(studentIDs) > 200 {
		return TutorAssignments{}, domain.Validation(map[string]string{"studentIds": "Elige entre 1 y 200 estudiantes."})
	}
	if periodID == "" {
		return TutorAssignments{}, domain.ErrNotFound
	}
	if staffID != "" {
		candidates, err := s.tutoring.TutorCandidates(ctx, sc.institution.ID)
		if err != nil {
			return TutorAssignments{}, err
		}
		if !slices.ContainsFunc(candidates, func(c domain.TutorCandidate) bool { return c.StaffID == staffID }) {
			return TutorAssignments{}, domain.Validation(map[string]string{"staffId": "Elige un docente o tutor activo."})
		}
	}
	n, err := s.tutoring.AssignTutor(ctx, sc.institution.ID, periodID, studentIDs, staffID, sc.name())
	if err != nil {
		return TutorAssignments{}, err
	}
	summary := fmt.Sprintf("Asignó tutor propio a %d %s", n, plural(n, "estudiante", "estudiantes"))
	if staffID == "" {
		summary = fmt.Sprintf("Devolvió %d %s al tutor de su sección", n, plural(n, "estudiante", "estudiantes"))
	}
	s.tutorAudit(ctx, sc, AuditEntry{Action: domain.AuditUpdate, Entity: "tutor_assignment", EntityID: staffID, Summary: summary})
	s.tutoringChanged(ctx, sc, "assignments", "")
	return s.TutorAssignments(ctx, token, slug)
}

// ---------- sessions ----------

var (
	tutoringKinds  = map[string]bool{domain.SessionIndividual: true, domain.SessionGroup: true}
	sessionModes   = map[string]bool{"virtual": true, "in_person": true, "phone": true}
	sessionAlerts  = map[string]bool{"": true, "inactive": true, "overdue": true, "low_permanence": true}
	closedStatuses = map[string]bool{domain.SessionMissed: true, domain.SessionCancelled: true}
)

type SessionInput struct {
	ID          string
	StaffID     string
	Kind        string
	Mode        string
	ScheduledAt time.Time
	DurationMin int
	Link        string
	Reason      string
	AlertKind   string
	StudentIDs  []string
}

func validLink(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// SaveTutoringSession schedules a session (or changes one not held yet) with the students called. A tutor schedules
// with their own students; dirección and coordinación with anyone, for any tutor
func (s *WellbeingService) SaveTutoringSession(ctx context.Context, token, slug string, in SessionInput) (domain.TutoringSession, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return domain.TutoringSession{}, err
	}
	if sc.family() {
		return domain.TutoringSession{}, domain.ErrWellbeingForbidden
	}
	in.Reason, in.Link = strings.TrimSpace(in.Reason), strings.TrimSpace(in.Link)
	in.StudentIDs = slices.Compact(slices.Sorted(slices.Values(in.StudentIDs)))
	errs := map[string]string{}
	switch {
	case !tutoringKinds[in.Kind]:
		errs["kind"] = "Elige individual o grupal."
	case in.Kind == domain.SessionIndividual && len(in.StudentIDs) != 1:
		errs["studentIds"] = "La tutoría individual es con un estudiante."
	case len(in.StudentIDs) == 0 || len(in.StudentIDs) > 60:
		errs["studentIds"] = "Elige entre 1 y 60 estudiantes."
	}
	if !sessionModes[in.Mode] {
		errs["mode"] = "Elige virtual, presencial o por teléfono."
	}
	if in.Link != "" && !validLink(in.Link) {
		errs["link"] = "El enlace empieza con https://."
	}
	if n := len([]rune(in.Reason)); n < 3 || n > 300 {
		errs["reason"] = "Escribe el motivo (hasta 300 caracteres)."
	}
	if in.DurationMin < 5 || in.DurationMin > 240 {
		errs["durationMin"] = "La duración va de 5 a 240 minutos."
	}
	if in.ScheduledAt.IsZero() {
		errs["scheduledAt"] = "Indica la fecha y la hora."
	}
	if !sessionAlerts[in.AlertKind] {
		errs["alertKind"] = "Alerta desconocida."
	}
	if len(errs) > 0 {
		return domain.TutoringSession{}, domain.Validation(errs)
	}
	staffID := sc.staffID
	if sc.office() && in.StaffID != "" {
		staffID = in.StaffID
	}
	if staffID == "" {
		return domain.TutoringSession{}, domain.Validation(map[string]string{"staffId": "Elige el tutor de la sesión."})
	}
	if in.ID != "" {
		old, err := s.tutoring.Session(ctx, sc.institution.ID, in.ID)
		if err != nil {
			return old, err
		}
		if !sc.office() && old.StaffID != sc.staffID {
			return old, domain.ErrNotTutor
		}
		if old.Status == domain.SessionHeld {
			return old, domain.ErrSessionClosed
		}
		staffID = old.StaffID
	}
	// Every student called must be the tutor's (the office calls anyone enrolled)
	all, err := s.tutoring.TutorStudents(ctx, sc.institution.ID, periodID, "")
	if err != nil {
		return domain.TutoringSession{}, err
	}
	for _, id := range in.StudentIDs {
		i := slices.IndexFunc(all, func(t domain.TutorStudent) bool { return t.StudentID == id })
		if i < 0 || (!sc.office() && all[i].StaffID != staffID) {
			return domain.TutoringSession{}, domain.ErrNotTutor
		}
	}
	x, err := s.tutoring.SaveSession(ctx, sc.institution.ID, domain.TutoringSession{ID: in.ID, StaffID: staffID, Kind: in.Kind, Mode: in.Mode, ScheduledAt: in.ScheduledAt,
		DurationMin: in.DurationMin, Link: in.Link, Reason: in.Reason, AlertKind: in.AlertKind, CreatedName: sc.name()}, in.StudentIDs)
	if err != nil {
		return x, err
	}
	action := domain.AuditCreate
	if in.ID != "" {
		action = domain.AuditUpdate
	}
	s.tutorAudit(ctx, sc, AuditEntry{Action: action, Entity: "tutoring_session", EntityID: x.ID,
		Summary: fmt.Sprintf("Agendó una tutoría %s para el %s: %s", map[string]string{domain.SessionIndividual: "individual", domain.SessionGroup: "grupal"}[x.Kind],
			x.ScheduledAt.In(lima()).Format("02/01/2006 15:04"), x.Reason)})
	s.tutoringChanged(ctx, sc, "sessions", "")
	return x, nil
}

type HoldSessionInput struct {
	ID         string
	Attended   []string
	Notes      string
	Agreements string
	// A commitment for each student who came, due on CommitmentDue (empty description: none)
	Commitment    string
	CommitmentDue *time.Time
}

// HoldTutoringSession records the session: who came (each gets an interview in their file, which also counts as
// activity for the permanence), the notes and agreements, and a commitment to follow up. With nobody present it
// is recorded as missed
func (s *WellbeingService) HoldTutoringSession(ctx context.Context, token, slug string, in HoldSessionInput) (domain.TutoringSession, error) {
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return domain.TutoringSession{}, err
	}
	x, err := s.tutoring.Session(ctx, sc.institution.ID, in.ID)
	if err != nil {
		return x, err
	}
	if !sc.office() && x.StaffID != sc.staffID {
		return x, domain.ErrNotTutor
	}
	if x.Status == domain.SessionHeld {
		return x, domain.ErrSessionClosed
	}
	if x.ScheduledAt.After(s.attendance.now().Add(30 * time.Minute)) {
		return x, domain.Validation(map[string]string{"scheduledAt": "Registra la tutoría cuando ya se haya realizado."})
	}
	in.Notes, in.Agreements, in.Commitment = strings.TrimSpace(in.Notes), strings.TrimSpace(in.Agreements), strings.TrimSpace(in.Commitment)
	errs := map[string]string{}
	if tooLong(in.Notes, 4000) {
		errs["notes"] = "Las notas van hasta 4000 caracteres."
	}
	if tooLong(in.Agreements, 2000) {
		errs["agreements"] = "Los acuerdos van hasta 2000 caracteres."
	}
	if tooLong(in.Commitment, 500) {
		errs["commitment"] = "El compromiso va hasta 500 caracteres."
	}
	if in.Commitment != "" && in.CommitmentDue == nil {
		errs["commitmentDue"] = "Indica para cuándo es el compromiso."
	}
	for _, id := range in.Attended {
		if !slices.ContainsFunc(x.Attendees, func(a domain.SessionAttendee) bool { return a.StudentID == id }) {
			errs["attended"] = "Solo asisten los estudiantes convocados."
		}
	}
	if len(errs) > 0 {
		return x, domain.Validation(errs)
	}
	if len(in.Attended) == 0 {
		if err := s.tutoring.SetSessionStatus(ctx, sc.institution.ID, x.ID, domain.SessionMissed); err != nil {
			return x, err
		}
		s.tutoringChanged(ctx, sc, "sessions", "")
		return s.tutoring.Session(ctx, sc.institution.ID, x.ID)
	}
	now := s.attendance.now()
	topic := x.Reason
	if r := []rune(topic); len(r) > 160 {
		topic = string(r[:160])
	}
	x, err = s.tutoring.HoldSession(ctx, sc.institution.ID, port.HoldInput{SessionID: x.ID, Attended: in.Attended, Notes: in.Notes, Agreements: in.Agreements,
		HeldOn: dayOf(x.ScheduledAt.In(lima())), Topic: topic, AuthorID: sc.userID, AuthorName: sc.name(), At: now})
	if err != nil {
		return x, err
	}
	if in.Commitment != "" {
		for _, a := range x.Attendees {
			if a.Attended == nil || !*a.Attended {
				continue
			}
			if _, err := s.repo.SaveCommitment(ctx, sc.institution.ID, domain.Commitment{StudentID: a.StudentID, InterviewID: a.InterviewID, Party: "student",
				Description: in.Commitment, DueOn: *in.CommitmentDue, Status: "pending", AuthorName: sc.name()}); err != nil {
				return x, err
			}
		}
	}
	s.tutorAudit(ctx, sc, AuditEntry{Action: domain.AuditStatus, Entity: "tutoring_session", EntityID: x.ID,
		Summary: fmt.Sprintf("Registró la tutoría «%s» con %d %s", topic, len(in.Attended), plural(len(in.Attended), "asistente", "asistentes"))})
	s.tutoringChanged(ctx, sc, "sessions", "")
	s.changed(ctx, sc, "tutoring", "")
	return x, nil
}

// SetTutoringSessionStatus marks a session not held as missed or cancelled (or back to scheduled)
func (s *WellbeingService) SetTutoringSessionStatus(ctx context.Context, token, slug, id, status string) (domain.TutoringSession, error) {
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return domain.TutoringSession{}, err
	}
	if !closedStatuses[status] && status != domain.SessionScheduled {
		return domain.TutoringSession{}, domain.Validation(map[string]string{"status": "Elige no asistió, cancelada o agendada."})
	}
	x, err := s.tutoring.Session(ctx, sc.institution.ID, id)
	if err != nil {
		return x, err
	}
	if !sc.office() && x.StaffID != sc.staffID {
		return x, domain.ErrNotTutor
	}
	if err := s.tutoring.SetSessionStatus(ctx, sc.institution.ID, id, status); err != nil {
		return x, err
	}
	s.tutoringChanged(ctx, sc, "sessions", "")
	return s.tutoring.Session(ctx, sc.institution.ID, id)
}

// TutoringSessions lists the sessions between two days: the tutor's own, any tutor's for the office (staffID)
func (s *WellbeingService) TutoringSessions(ctx context.Context, token, slug, staffID string, from, to time.Time) ([]domain.TutoringSession, error) {
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if sc.family() {
		return nil, domain.ErrWellbeingForbidden
	}
	target := sc.staffID
	if sc.office() {
		target = staffID
	} else if target == "" {
		return nil, domain.ErrNotTutor
	}
	return s.tutoring.Sessions(ctx, domain.SessionFilter{InstitutionID: sc.institution.ID, StaffID: target, From: &from, To: &to})
}

// ---------- messages ----------

type ThreadView struct {
	Thread   domain.Thread
	Messages []domain.Message
	// The viewer's side (staff | student); empty: reads without writing (dirección)
	Side string
}

// student is the student account's own student id ("" when the viewer is not a student)
func (sc wellScope) ownStudent() string {
	if sc.role == "student" && len(sc.children) == 1 {
		return sc.children[0]
	}
	return ""
}

// threadSide is the viewer's side in the thread ("" when they only read it, error when they may not)
func (sc wellScope) threadSide(t domain.Thread) (string, error) {
	switch {
	case sc.staffID != "" && t.StaffID == sc.staffID:
		return domain.SideStaff, nil
	case sc.ownStudent() != "" && t.StudentID == sc.ownStudent():
		return domain.SideStudent, nil
	case sc.office():
		return "", nil
	}
	return "", domain.ErrMessagesForbidden
}

const messagePage = 200

func (s *WellbeingService) threadView(ctx context.Context, sc wellScope, t domain.Thread, side string) (ThreadView, error) {
	msgs, err := s.tutoring.Messages(ctx, t.ID, messagePage)
	if err != nil {
		return ThreadView{}, err
	}
	if side != "" {
		if err := s.tutoring.MarkRead(ctx, t.ID, side, s.attendance.now()); err != nil {
			return ThreadView{}, err
		}
		t.Unread = 0
	}
	return ThreadView{Thread: t, Messages: msgs, Side: side}, nil
}

// OpenThread opens the conversation with a student (staff: the tutor in force or the office with a staff record) or,
// for a student account, with their tutor; it is created on first use
func (s *WellbeingService) OpenThread(ctx context.Context, token, slug, studentID string) (ThreadView, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return ThreadView{}, err
	}
	var studentOf, staffOf string
	switch {
	case sc.ownStudent() != "":
		t, err := s.tutorOf(ctx, sc, periodID, sc.ownStudent())
		if err != nil || t.StaffID == "" {
			return ThreadView{}, domain.ErrNoTutor
		}
		studentOf, staffOf = t.StudentID, t.StaffID
	case sc.family():
		return ThreadView{}, domain.ErrMessagesForbidden
	default:
		t, err := s.tutorOf(ctx, sc, periodID, studentID)
		if err != nil {
			return ThreadView{}, err
		}
		if !sc.tutorsStudent(t) || sc.staffID == "" {
			return ThreadView{}, domain.ErrMessagesForbidden
		}
		studentOf, staffOf = t.StudentID, sc.staffID
	}
	t, err := s.tutoring.EnsureThread(ctx, sc.institution.ID, studentOf, staffOf)
	if err != nil {
		return ThreadView{}, err
	}
	side, err := sc.threadSide(t)
	if err != nil {
		return ThreadView{}, err
	}
	return s.threadView(ctx, sc, t, side)
}

// ThreadMessages reads a conversation and marks it read for the viewer's side
func (s *WellbeingService) ThreadMessages(ctx context.Context, token, slug, threadID string) (ThreadView, error) {
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return ThreadView{}, err
	}
	t, err := s.tutoring.Thread(ctx, sc.institution.ID, threadID)
	if err != nil {
		return ThreadView{}, err
	}
	side, err := sc.threadSide(t)
	if err != nil {
		return ThreadView{}, err
	}
	return s.threadView(ctx, sc, t, side)
}

// SendMessage writes in a conversation the viewer takes part in
func (s *WellbeingService) SendMessage(ctx context.Context, token, slug, threadID, body string) (domain.Message, error) {
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return domain.Message{}, err
	}
	body = strings.TrimSpace(body)
	if body == "" || tooLong(body, 2000) {
		return domain.Message{}, domain.Validation(map[string]string{"body": "Escribe el mensaje (hasta 2000 caracteres)."})
	}
	t, err := s.tutoring.Thread(ctx, sc.institution.ID, threadID)
	if err != nil {
		return domain.Message{}, err
	}
	side, err := sc.threadSide(t)
	if err != nil {
		return domain.Message{}, err
	}
	if side == "" {
		return domain.Message{}, domain.ErrMessagesForbidden
	}
	m, err := s.tutoring.AddMessage(ctx, t.ID, domain.Message{Side: side, AuthorName: sc.name(), Body: body}, sc.userID)
	if err != nil {
		return m, err
	}
	s.tutoringChanged(ctx, sc, "messages", t.ID)
	return m, nil
}

// Threads are the viewer's conversations: a staff member's with their students, a student's with their tutor
func (s *WellbeingService) Threads(ctx context.Context, token, slug string) ([]domain.Thread, error) {
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	switch {
	case sc.ownStudent() != "":
		return s.tutoring.Threads(ctx, sc.institution.ID, "", sc.ownStudent())
	case sc.staffID != "":
		return s.tutoring.Threads(ctx, sc.institution.ID, sc.staffID, "")
	}
	return []domain.Thread{}, nil
}

// ---------- the student's view ----------

type MyTutoring struct {
	Tutor    domain.TutorStudent
	HasTutor bool
	// Sessions the student is called to, without the tutor's notes
	Sessions []domain.TutoringSession
	Thread   *ThreadView
}

// MyTutoring is a student's tutoring: their tutor, their sessions (with the link to join) and the conversation
func (s *WellbeingService) MyTutoring(ctx context.Context, token, slug string) (MyTutoring, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return MyTutoring{}, err
	}
	me := sc.ownStudent()
	if me == "" {
		return MyTutoring{}, domain.ErrMessagesForbidden
	}
	out := MyTutoring{}
	t, err := s.tutorOf(ctx, sc, periodID, me)
	if err != nil && err != domain.ErrNotFound {
		return out, err
	}
	out.Tutor, out.HasTutor = t, err == nil && t.StaffID != ""
	from := s.today().AddDate(0, 0, -30)
	if out.Sessions, err = s.tutoring.Sessions(ctx, domain.SessionFilter{InstitutionID: sc.institution.ID, StudentID: me, From: &from}); err != nil {
		return out, err
	}
	for i := range out.Sessions {
		out.Sessions[i].Notes = ""
		out.Sessions[i].Attendees = slices.DeleteFunc(out.Sessions[i].Attendees, func(a domain.SessionAttendee) bool { return a.StudentID != me })
	}
	if out.HasTutor {
		v, err := s.OpenThread(ctx, token, slug, "")
		if err != nil {
			return out, err
		}
		out.Thread = &v
	}
	return out, nil
}

// ---------- SAANEE ----------

var saaneeStatuses = map[string]bool{domain.SaaneeSent: true, domain.SaaneeReceived: true, domain.SaaneeAttending: true, domain.SaaneeClosed: true}

// ReferSaanee refers a student to the SAANEE of the jurisdiction (the tutor in force or the office)
func (s *WellbeingService) ReferSaanee(ctx context.Context, token, slug, studentID, reason, reference string, sentOn time.Time) (domain.SaaneeReferral, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return domain.SaaneeReferral{}, err
	}
	t, err := s.tutorOf(ctx, sc, periodID, studentID)
	if err != nil {
		return domain.SaaneeReferral{}, err
	}
	if !sc.tutorsStudent(t) {
		return domain.SaaneeReferral{}, domain.ErrNotTutor
	}
	reason, reference = strings.TrimSpace(reason), strings.TrimSpace(reference)
	switch {
	case len([]rune(reason)) < 10 || tooLong(reason, 2000):
		return domain.SaaneeReferral{}, domain.Validation(map[string]string{"reason": "Describe lo observado (de 10 a 2000 caracteres)."})
	case tooLong(reference, 120):
		return domain.SaaneeReferral{}, domain.Validation(map[string]string{"reference": "La referencia va hasta 120 caracteres."})
	case sentOn.IsZero() || sentOn.After(s.today()):
		return domain.SaaneeReferral{}, domain.Validation(map[string]string{"sentOn": "Indica cuándo se envió (hoy o antes)."})
	}
	x, err := s.tutoring.CreateSaanee(ctx, sc.institution.ID, domain.SaaneeReferral{StudentID: studentID, Reason: reason, Reference: reference, SentOn: sentOn, ReferredName: sc.name()})
	if err != nil {
		return x, err
	}
	s.tutorAudit(ctx, sc, AuditEntry{Action: domain.AuditCreate, Entity: "saanee_referral", EntityID: x.ID, Summary: "Derivó al SAANEE a " + x.StudentName})
	s.tutoringChanged(ctx, sc, "saanee", "")
	return x, nil
}

// UpdateSaanee follows a referral up: its status, reference and the SAANEE's answer
func (s *WellbeingService) UpdateSaanee(ctx context.Context, token, slug, id, status, reference, response string) (domain.SaaneeReferral, error) {
	sc, periodID, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return domain.SaaneeReferral{}, err
	}
	x, err := s.tutoring.SaaneeReferral(ctx, sc.institution.ID, id)
	if err != nil {
		return x, err
	}
	if !sc.office() {
		t, err := s.tutorOf(ctx, sc, periodID, x.StudentID)
		if err != nil || !sc.tutorsStudent(t) {
			return x, domain.ErrNotTutor
		}
	}
	reference, response = strings.TrimSpace(reference), strings.TrimSpace(response)
	switch {
	case !saaneeStatuses[status]:
		return x, domain.Validation(map[string]string{"status": "Elige el estado."})
	case tooLong(reference, 120):
		return x, domain.Validation(map[string]string{"reference": "La referencia va hasta 120 caracteres."})
	case tooLong(response, 2000):
		return x, domain.Validation(map[string]string{"response": "La respuesta va hasta 2000 caracteres."})
	}
	x.Status, x.Reference, x.Response, x.UpdatedName = status, reference, response, sc.name()
	if x, err = s.tutoring.UpdateSaanee(ctx, sc.institution.ID, x); err != nil {
		return x, err
	}
	s.tutorAudit(ctx, sc, AuditEntry{Action: domain.AuditStatus, Entity: "saanee_referral", EntityID: x.ID, Summary: "Actualizó la derivación al SAANEE de " + x.StudentName})
	s.tutoringChanged(ctx, sc, "saanee", "")
	return x, nil
}
