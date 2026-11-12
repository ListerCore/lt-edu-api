package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventApafaChanged: {institution, area (meetings | board), meeting}
const EventApafaChanged = "apafa.changed"

var (
	// Dirección and secretaría convene and record the meetings; coordinación and the families read them
	apafaManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "secretary": true}
	apafaReaders  = map[string]bool{"academic_coordinator": true, "guardian": true}
	// The association exists in Educación Básica (Ley N.° 28628): schools, CEBA and CEBE
	apafaInstitutionKinds = map[string]bool{domain.KindEBR: true, domain.KindEBA: true, domain.KindEBE: true}

	apafaKinds      = map[string]bool{domain.ApafaOrdinary: true, domain.ApafaExtraordinary: true, domain.ApafaAssembly: true, domain.ApafaBoard: true}
	apafaModalities = map[string]bool{domain.ApafaInPerson: true, domain.ApafaVirtual: true, domain.ApafaHybrid: true}
	apafaAudiences  = map[string]bool{domain.AudienceGuardians: true, domain.AudienceCommunity: true}
	apafaPositions  = map[string]bool{"president": true, "vice_president": true, "secretary": true, "treasurer": true, "spokesperson": true}
	// The link of a virtual meeting must be a plain https address (Meet, Zoom, Teams…)
	meetingURLPattern = regexp.MustCompile(`^https://[^\s<>"]+$`)
	boardPhonePattern = regexp.MustCompile(`^[0-9+ ]{0,20}$`)
)

// Spanish names for the notices
var (
	apafaKindLabels = map[string]string{domain.ApafaOrdinary: "reunión ordinaria", domain.ApafaExtraordinary: "reunión extraordinaria", domain.ApafaAssembly: "asamblea general", domain.ApafaBoard: "reunión del consejo directivo"}
	esWeekdays      = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}
	esMonths        = []string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
)

// ApafaService runs the parents' association (APAFA) of public institutions: its meetings (convene, reschedule,
// cancel, record the acta), the board, and a read-only view of the APAFA fee collection. Every change families must
// know about goes out as a comms announcement, so it shows in Comunicados, the header ticker and live
type ApafaService struct {
	repo          port.ApafaRepository
	auth          *AuthService
	announcements *AnnouncementService
	live          *LiveService
	now           func() time.Time
}

func NewApafaService(repo port.ApafaRepository, auth *AuthService, announcements *AnnouncementService, live *LiveService) *ApafaService {
	return &ApafaService{repo: repo, auth: auth, announcements: announcements, live: live, now: time.Now}
}

type apafaScope struct {
	institutionScope
	userName string
}

func (sc apafaScope) manager() bool { return apafaManagers[sc.role] }

// family: guardians only read meetings already convened
func (sc apafaScope) family() bool { return sc.role == "guardian" }

// scope resolves the institution and checks role, institution type and public management
func (s *ApafaService) scope(ctx context.Context, token, slug string) (apafaScope, error) {
	isc, err := resolveInstitution(ctx, s.auth, s.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return apafaScope{}, err
	}
	if !apafaManagers[isc.role] && !apafaReaders[isc.role] {
		return apafaScope{}, domain.ErrApafaForbidden
	}
	if !apafaInstitutionKinds[isc.institution.Kind] {
		return apafaScope{}, domain.ErrApafaPrivate
	}
	management, err := s.repo.Management(ctx, isc.institution.ID)
	if err != nil {
		return apafaScope{}, err
	}
	if management != domain.ManagementPublic {
		return apafaScope{}, domain.ErrApafaPrivate
	}
	me, err := s.auth.Me(ctx, token)
	if err != nil {
		return apafaScope{}, err
	}
	return apafaScope{institutionScope: isc, userName: uploaderName(me.User)}, nil
}

func (s *ApafaService) manage(ctx context.Context, token, slug string) (apafaScope, error) {
	sc, err := s.scope(ctx, token, slug)
	if err == nil && !sc.manager() {
		return sc, domain.ErrApafaReadOnly
	}
	return sc, err
}

func (s *ApafaService) changed(ctx context.Context, sc apafaScope, area, meetingID string) {
	s.live.Notify(ctx, EventApafaChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "meeting": meetingID})
}

// ---------- reading ----------

// ApafaOverview is the APAFA page: the meetings (newest first), the board and, for staff, the APAFA fee collection
type ApafaOverview struct {
	Meetings  []domain.ApafaMeeting
	Board     []domain.ApafaBoardMember
	Fees      *domain.ApafaFees
	CanManage bool
}

func (s *ApafaService) Overview(ctx context.Context, token, slug string) (ApafaOverview, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ApafaOverview{}, err
	}
	meetings, err := s.repo.Meetings(ctx, sc.institution.ID, !sc.family())
	if err != nil {
		return ApafaOverview{}, err
	}
	board, err := s.repo.Board(ctx, sc.institution.ID)
	if err != nil {
		return ApafaOverview{}, err
	}
	out := ApafaOverview{Meetings: meetings, Board: board, CanManage: sc.manager()}
	if !sc.family() {
		fees, err := s.repo.Fees(ctx, sc.institution.ID)
		if err != nil {
			return ApafaOverview{}, err
		}
		out.Fees = &fees
	}
	return out, nil
}

// ApafaMeetingDetail is one meeting and whether the person may change it
type ApafaMeetingDetail struct {
	Meeting   domain.ApafaMeeting
	CanManage bool
}

// meeting loads one meeting of the institution; families never see one not convened yet
func (s *ApafaService) meeting(ctx context.Context, sc apafaScope, id string) (domain.ApafaMeeting, error) {
	if !attachmentIDPattern.MatchString(id) {
		return domain.ApafaMeeting{}, domain.ErrNotFound
	}
	m, err := s.repo.Meeting(ctx, sc.institution.ID, id)
	if err != nil {
		return m, err
	}
	if sc.family() && m.NotifiedAt == nil {
		return domain.ApafaMeeting{}, domain.ErrNotFound
	}
	return m, nil
}

func (s *ApafaService) Meeting(ctx context.Context, token, slug, id string) (ApafaMeetingDetail, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ApafaMeetingDetail{}, err
	}
	m, err := s.meeting(ctx, sc, id)
	if err != nil {
		return ApafaMeetingDetail{}, err
	}
	return ApafaMeetingDetail{Meeting: m, CanManage: sc.manager()}, nil
}

// ---------- meetings ----------

// MeetingInput is what the panel sends to schedule or edit a meeting
type MeetingInput struct {
	Title       string
	Kind        string
	ScheduledAt time.Time
	Modality    string
	Place       string
	MeetingURL  string
	Agenda      string
	Audience    string
}

func runeLen(v string) int { return utf8.RuneCountInString(v) }

// validateMeeting cleans and checks the fields; future=true asks for a date still to come (new meetings and new dates)
func validateMeeting(in MeetingInput, now time.Time, future bool) (domain.ApafaMeeting, map[string]string) {
	m := domain.ApafaMeeting{Title: strings.TrimSpace(in.Title), Kind: in.Kind, ScheduledAt: in.ScheduledAt, Modality: in.Modality, Place: strings.TrimSpace(in.Place),
		MeetingURL: strings.TrimSpace(in.MeetingURL), Agenda: strings.TrimSpace(in.Agenda), Audience: in.Audience}
	if m.Modality == "" {
		m.Modality = domain.ApafaInPerson
	}
	if m.Audience == "" {
		m.Audience = domain.AudienceGuardians
	}
	fields := map[string]string{}
	if n := runeLen(m.Title); n < 5 || n > 140 {
		fields["title"] = "El título debe tener entre 5 y 140 caracteres."
	}
	if !apafaKinds[m.Kind] {
		fields["kind"] = "Elige el tipo de reunión."
	}
	switch {
	case m.ScheduledAt.IsZero():
		fields["scheduled_at"] = "Indica la fecha y la hora."
	case future && !m.ScheduledAt.After(now):
		fields["scheduled_at"] = "La fecha ya pasó: elige una fecha y hora futuras."
	case m.ScheduledAt.After(now.AddDate(2, 0, 0)):
		fields["scheduled_at"] = "La fecha no puede pasar de dos años."
	}
	if !apafaModalities[m.Modality] {
		fields["modality"] = "Elige si es presencial, virtual o mixta."
	}
	needsPlace := m.Modality != domain.ApafaVirtual
	needsURL := m.Modality != domain.ApafaInPerson
	switch {
	case needsPlace && m.Place == "":
		fields["place"] = "Indica el lugar."
	case runeLen(m.Place) > 200:
		fields["place"] = "El lugar puede tener hasta 200 caracteres."
	}
	switch {
	case needsURL && m.MeetingURL == "":
		fields["meeting_url"] = "Pega el enlace de la reunión virtual."
	case m.MeetingURL != "" && (len(m.MeetingURL) > 500 || !meetingURLPattern.MatchString(m.MeetingURL)):
		fields["meeting_url"] = "Usa un enlace que empiece con https://."
	}
	if !needsPlace {
		m.Place = ""
	}
	if !needsURL {
		m.MeetingURL = ""
	}
	if runeLen(m.Agenda) > 4000 {
		fields["agenda"] = "La agenda puede tener hasta 4000 caracteres."
	}
	if !apafaAudiences[m.Audience] {
		fields["audience"] = "Elige a quién se convoca."
	}
	return m, fields
}

// SaveMeeting schedules a new meeting (id empty) or edits one still to be held. notify convenes it (a draft) or tells
// the families what changed (one already convened). A convened meeting changes its date only through Reschedule
func (s *ApafaService) SaveMeeting(ctx context.Context, token, slug, id string, in MeetingInput, notify bool) (domain.ApafaMeeting, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.ApafaMeeting{}, err
	}
	now := s.now()
	if id == "" {
		draft, fields := validateMeeting(in, now, true)
		if len(fields) > 0 {
			return domain.ApafaMeeting{}, domain.Validation(fields)
		}
		draft.InstitutionID, draft.CreatedBy, draft.CreatedName = sc.institution.ID, sc.userID, sc.userName
		created, err := s.repo.CreateMeeting(ctx, draft)
		if err != nil {
			return domain.ApafaMeeting{}, err
		}
		if notify {
			created, err = s.publish(ctx, sc, created, noticeConvene)
		}
		s.changed(ctx, sc, "meetings", created.ID)
		return created, err
	}
	current, err := s.meeting(ctx, sc, id)
	if err != nil {
		return domain.ApafaMeeting{}, err
	}
	if !current.Open() {
		return domain.ApafaMeeting{}, domain.ErrApafaClosed
	}
	sameDate := in.ScheduledAt.Equal(current.ScheduledAt)
	draft, fields := validateMeeting(in, now, !sameDate)
	if current.NotifiedAt != nil && !sameDate {
		fields["scheduled_at"] = "La reunión ya fue convocada: para cambiar la fecha usa «Reprogramar»."
	}
	if len(fields) > 0 {
		return domain.ApafaMeeting{}, domain.Validation(fields)
	}
	next := current
	next.Title, next.Kind, next.ScheduledAt, next.Modality, next.Place, next.MeetingURL, next.Agenda, next.Audience =
		draft.Title, draft.Kind, draft.ScheduledAt, draft.Modality, draft.Place, draft.MeetingURL, draft.Agenda, draft.Audience
	updated, err := s.repo.UpdateMeeting(ctx, next)
	if err != nil {
		return domain.ApafaMeeting{}, err
	}
	if notify {
		kind := noticeUpdate
		if updated.NotifiedAt == nil {
			kind = noticeConvene
		}
		updated, err = s.publish(ctx, sc, updated, kind)
	}
	s.changed(ctx, sc, "meetings", updated.ID)
	return updated, err
}

// Convene sends the convocatoria of a meeting saved without notifying
func (s *ApafaService) Convene(ctx context.Context, token, slug, id string) (domain.ApafaMeeting, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.ApafaMeeting{}, err
	}
	m, err := s.meeting(ctx, sc, id)
	if err != nil {
		return m, err
	}
	switch {
	case !m.Open():
		return m, domain.ErrApafaClosed
	case m.NotifiedAt != nil:
		return m, domain.ErrApafaNotified
	case !m.ScheduledAt.After(s.now()):
		return m, domain.Validation(map[string]string{"scheduled_at": "La fecha ya pasó: reprograma la reunión antes de convocarla."})
	}
	m, err = s.publish(ctx, sc, m, noticeConvene)
	s.changed(ctx, sc, "meetings", m.ID)
	return m, err
}

// Reschedule moves a meeting still to be held to a new date, keeping the old one and the reason
func (s *ApafaService) Reschedule(ctx context.Context, token, slug, id string, at time.Time, reason string, notify bool) (domain.ApafaMeeting, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.ApafaMeeting{}, err
	}
	m, err := s.meeting(ctx, sc, id)
	if err != nil {
		return m, err
	}
	if !m.Open() {
		return m, domain.ErrApafaClosed
	}
	reason = strings.TrimSpace(reason)
	fields := map[string]string{}
	now := s.now()
	switch {
	case at.IsZero():
		fields["scheduled_at"] = "Indica la nueva fecha y hora."
	case !at.After(now):
		fields["scheduled_at"] = "La nueva fecha ya pasó: elige una futura."
	case at.Equal(m.ScheduledAt):
		fields["scheduled_at"] = "Es la misma fecha de ahora."
	case at.After(now.AddDate(2, 0, 0)):
		fields["scheduled_at"] = "La fecha no puede pasar de dos años."
	}
	if n := runeLen(reason); n < 5 || n > 300 {
		fields["reason"] = "Explica el motivo en 5 a 300 caracteres."
	}
	if len(fields) > 0 {
		return m, domain.Validation(fields)
	}
	previous := m.ScheduledAt
	m.PreviousAt, m.ScheduledAt, m.RescheduleReason, m.Status = &previous, at, reason, domain.ApafaRescheduled
	if m, err = s.repo.UpdateMeeting(ctx, m); err != nil {
		return m, err
	}
	// A draft was never announced: nothing to tell the families yet
	if notify && m.NotifiedAt != nil {
		m, err = s.publish(ctx, sc, m, noticeReschedule)
	}
	s.changed(ctx, sc, "meetings", m.ID)
	return m, err
}

// Cancel calls off a meeting still to be held
func (s *ApafaService) Cancel(ctx context.Context, token, slug, id, reason string, notify bool) (domain.ApafaMeeting, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.ApafaMeeting{}, err
	}
	m, err := s.meeting(ctx, sc, id)
	if err != nil {
		return m, err
	}
	if !m.Open() {
		return m, domain.ErrApafaClosed
	}
	reason = strings.TrimSpace(reason)
	if n := runeLen(reason); n < 5 || n > 300 {
		return m, domain.Validation(map[string]string{"reason": "Explica el motivo en 5 a 300 caracteres."})
	}
	m.Status, m.CancelReason = domain.ApafaCancelled, reason
	if m, err = s.repo.UpdateMeeting(ctx, m); err != nil {
		return m, err
	}
	if notify && m.NotifiedAt != nil {
		m, err = s.publish(ctx, sc, m, noticeCancel)
	}
	s.changed(ctx, sc, "meetings", m.ID)
	return m, err
}

// MinutesInput is the record of a meeting once held: summary of the acta, agreements and how many attended
type MinutesInput struct {
	Minutes    string
	Agreements string
	Attendees  *int
}

func validateMinutes(in MinutesInput) (MinutesInput, map[string]string) {
	out := MinutesInput{Minutes: strings.TrimSpace(in.Minutes), Agreements: strings.TrimSpace(in.Agreements), Attendees: in.Attendees}
	fields := map[string]string{}
	if n := runeLen(out.Minutes); n < 10 || n > 6000 {
		fields["minutes"] = "Resume el acta en 10 a 6000 caracteres."
	}
	if runeLen(out.Agreements) > 4000 {
		fields["agreements"] = "Los acuerdos pueden tener hasta 4000 caracteres."
	}
	if out.Attendees != nil && (*out.Attendees < 0 || *out.Attendees > 100000) {
		fields["attendees"] = "Indica un número de asistentes válido."
	}
	return out, fields
}

// RecordMinutes marks the meeting held (its time must have come) and stores the acta; publish tells the families
// and opens the meeting to them even when it was never convened through the panel
func (s *ApafaService) RecordMinutes(ctx context.Context, token, slug, id string, in MinutesInput, publish bool) (domain.ApafaMeeting, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.ApafaMeeting{}, err
	}
	m, err := s.meeting(ctx, sc, id)
	if err != nil {
		return m, err
	}
	if m.Status == domain.ApafaCancelled {
		return m, domain.ErrApafaNotHeld
	}
	clean, fields := validateMinutes(in)
	if m.ScheduledAt.After(s.now()) {
		fields["minutes"] = "La reunión todavía no llega a su fecha: regístrala cuando se haya realizado (o reprográmala)."
	}
	if len(fields) > 0 {
		return m, domain.Validation(fields)
	}
	m.Status, m.Minutes, m.Agreements, m.Attendees = domain.ApafaHeld, clean.Minutes, clean.Agreements, clean.Attendees
	if m, err = s.repo.UpdateMeeting(ctx, m); err != nil {
		return m, err
	}
	if publish {
		kind := noticeMinutes
		if m.MinutesAt != nil {
			kind = noticeMinutesUpdate
		}
		m, err = s.publish(ctx, sc, m, kind)
	}
	s.changed(ctx, sc, "meetings", m.ID)
	return m, err
}

// DeleteMeeting removes a draft: never convened and without files (a convened one is cancelled instead)
func (s *ApafaService) DeleteMeeting(ctx context.Context, token, slug, id string) error {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return err
	}
	m, err := s.meeting(ctx, sc, id)
	if err != nil {
		return err
	}
	if m.NotifiedAt != nil || m.Files > 0 {
		return domain.ErrApafaHasFiles
	}
	if err := s.repo.DeleteMeeting(ctx, sc.institution.ID, m.ID); err != nil {
		return err
	}
	s.changed(ctx, sc, "meetings", m.ID)
	return nil
}

// ---------- notices ----------

type noticeKind int

const (
	noticeConvene noticeKind = iota
	noticeUpdate
	noticeReschedule
	noticeCancel
	noticeMinutes
	noticeMinutesUpdate
)

// apafaWhen writes "jueves 20 de noviembre de 2026, 6:00 p. m." in Lima time
func apafaWhen(t time.Time) string {
	l := t.In(lima())
	h := l.Hour() % 12
	if h == 0 {
		h = 12
	}
	suffix := "a. m."
	if l.Hour() >= 12 {
		suffix = "p. m."
	}
	return fmt.Sprintf("%s %d de %s de %d, %d:%02d %s", esWeekdays[l.Weekday()], l.Day(), esMonths[l.Month()], l.Year(), h, l.Minute(), suffix)
}

// clip cuts a text to n runes, with an ellipsis when it was longer
func clip(v string, n int) string {
	r := []rune(v)
	if len(r) <= n {
		return v
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// apafaNotice builds the announcement of each event of a meeting. The texts are plain (Comunicados shows the body
// as is) and point to the APAFA page, where the agenda and the documents are
func apafaNotice(m domain.ApafaMeeting, kind noticeKind) (title, body string) {
	who := "a todas las familias"
	if m.Audience == domain.AudienceCommunity {
		who = "a toda la comunidad educativa"
	}
	where := func() string {
		var b strings.Builder
		if m.Place != "" {
			b.WriteString("\nLugar: " + m.Place)
		}
		if m.MeetingURL != "" {
			b.WriteString("\nEnlace: " + m.MeetingURL)
		}
		return b.String()
	}
	agenda := ""
	if m.Agenda != "" {
		agenda = "\n\nAgenda:\n" + m.Agenda
	}
	label := apafaKindLabels[m.Kind]
	const more = "\n\nLa agenda y los documentos de la reunión están en el panel, en APAFA."
	var b strings.Builder
	switch kind {
	case noticeConvene:
		title = "Convocatoria APAFA: " + m.Title
		fmt.Fprintf(&b, "La APAFA convoca %s a la %s «%s».\n\nFecha: %s%s%s%s", who, label, m.Title, apafaWhen(m.ScheduledAt), where(), agenda, more)
	case noticeUpdate:
		title = "Cambios en la reunión APAFA: " + m.Title
		fmt.Fprintf(&b, "Se actualizaron los datos de la %s «%s».\n\nFecha: %s%s%s%s", label, m.Title, apafaWhen(m.ScheduledAt), where(), agenda, more)
	case noticeReschedule:
		title = "Reunión APAFA reprogramada: " + m.Title
		before := ""
		if m.PreviousAt != nil {
			before = " prevista para el " + apafaWhen(*m.PreviousAt)
		}
		fmt.Fprintf(&b, "La %s «%s»%s se reprogramó.\n\nNueva fecha: %s%s\nMotivo: %s%s", label, m.Title, before, apafaWhen(m.ScheduledAt), where(), m.RescheduleReason, more)
	case noticeCancel:
		title = "Reunión APAFA cancelada: " + m.Title
		fmt.Fprintf(&b, "Se canceló la %s «%s» del %s.\n\nMotivo: %s", label, m.Title, apafaWhen(m.ScheduledAt), m.CancelReason)
	case noticeMinutes, noticeMinutesUpdate:
		title = "Acta de la reunión APAFA: " + m.Title
		verb := "Ya está disponible el acta"
		if kind == noticeMinutesUpdate {
			title = "Acta actualizada APAFA: " + m.Title
			verb = "Se actualizó el acta"
		}
		fmt.Fprintf(&b, "%s de la %s «%s» del %s.", verb, label, m.Title, apafaWhen(m.ScheduledAt))
		if m.Attendees != nil {
			fmt.Fprintf(&b, "\nAsistentes: %d.", *m.Attendees)
		}
		fmt.Fprintf(&b, "\n\nResumen:\n%s", m.Minutes)
		if m.Agreements != "" {
			fmt.Fprintf(&b, "\n\nAcuerdos:\n%s", m.Agreements)
		}
		b.WriteString("\n\nEl acta completa y los demás documentos están en el panel, en APAFA.")
	}
	return clip(title, 160), clip(b.String(), 5000)
}

// publish sends the notice of the event and links it to the meeting. The meeting change is already stored: when
// the announcement fails the caller gets the error but the meeting stays as saved
func (s *ApafaService) publish(ctx context.Context, sc apafaScope, m domain.ApafaMeeting, kind noticeKind) (domain.ApafaMeeting, error) {
	title, body := apafaNotice(m, kind)
	a, err := s.announcements.PublishNotice(ctx, NoticeInput{InstitutionID: sc.institution.ID, InstitutionSlug: sc.institution.Slug, AuthorID: sc.userID,
		Kind: domain.KindNotice, Audience: m.Audience, Title: title, Body: body})
	if err != nil {
		return m, errors.Join(domain.ErrApafaNoticeFailed, err)
	}
	now := s.now()
	m.AnnouncementID = a.ID
	if m.NotifiedAt == nil {
		m.NotifiedAt = &now
	}
	if kind == noticeMinutes || kind == noticeMinutesUpdate {
		m.MinutesAt = &now
	}
	return s.repo.UpdateMeeting(ctx, m)
}

// ---------- board ----------

// BoardMemberInput is one office of the board as the panel sends it
type BoardMemberInput struct {
	FullName string
	Position string
	Phone    string
	StartsOn time.Time
	EndsOn   time.Time
}

func validateBoardMember(in BoardMemberInput) (domain.ApafaBoardMember, map[string]string) {
	b := domain.ApafaBoardMember{FullName: strings.Join(strings.Fields(in.FullName), " "), Position: in.Position, Phone: strings.TrimSpace(in.Phone), StartsOn: in.StartsOn, EndsOn: in.EndsOn}
	fields := map[string]string{}
	if n := runeLen(b.FullName); n < 3 || n > 160 {
		fields["full_name"] = "Escribe el nombre completo."
	}
	if !apafaPositions[b.Position] {
		fields["position"] = "Elige el cargo."
	}
	if !boardPhonePattern.MatchString(b.Phone) {
		fields["phone"] = "Usa solo números (hasta 20)."
	}
	switch {
	case b.StartsOn.IsZero():
		fields["starts_on"] = "Indica desde cuándo."
	case b.EndsOn.IsZero():
		fields["ends_on"] = "Indica hasta cuándo."
	case b.EndsOn.Before(b.StartsOn):
		fields["ends_on"] = "El periodo debe terminar después de empezar."
	case b.EndsOn.After(b.StartsOn.AddDate(3, 0, 0)):
		fields["ends_on"] = "El periodo del consejo directivo no pasa de tres años."
	}
	return b, fields
}

// SaveBoardMember adds (id empty) or edits one office of the board
func (s *ApafaService) SaveBoardMember(ctx context.Context, token, slug, id string, in BoardMemberInput) (domain.ApafaBoardMember, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.ApafaBoardMember{}, err
	}
	if id != "" && !attachmentIDPattern.MatchString(id) {
		return domain.ApafaBoardMember{}, domain.ErrNotFound
	}
	b, fields := validateBoardMember(in)
	if len(fields) > 0 {
		return b, domain.Validation(fields)
	}
	b.ID, b.InstitutionID = id, sc.institution.ID
	saved, err := s.repo.SaveBoardMember(ctx, b)
	if err != nil {
		return saved, err
	}
	s.changed(ctx, sc, "board", "")
	return saved, nil
}

func (s *ApafaService) DeleteBoardMember(ctx context.Context, token, slug, id string) error {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return err
	}
	if !attachmentIDPattern.MatchString(id) {
		return domain.ErrNotFound
	}
	if err := s.repo.DeleteBoardMember(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "board", "")
	return nil
}
