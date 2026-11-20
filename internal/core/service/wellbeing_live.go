package service

import (
	"context"
	"slices"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Live tutoring sessions and psychology appointments, for the meeting service (clases en vivo)

// SetMeetings gives wellbeing the live sessions' store (psychology appointments)
func (s *WellbeingService) SetMeetings(r port.MeetingsRepository) { s.meetings = r }

var (
	appointmentModes    = map[string]bool{"virtual": true, "in_person": true}
	appointmentStatuses = map[string]bool{domain.SessionHeld: true, domain.SessionMissed: true, domain.SessionCancelled: true, domain.SessionScheduled: true}
)

func tutoringTarget(sc wellScope, x domain.TutoringSession) domain.LiveTarget {
	title := "Tutoría individual"
	if x.Kind == domain.SessionGroup {
		title = "Tutoría grupal"
	}
	return domain.LiveTarget{InstitutionID: sc.institution.ID, Source: domain.SourceTutoring, ID: x.ID, Title: title, Context: "Tutoría virtual",
		StartsAt: x.ScheduledAt, DurationMin: x.DurationMin, Provider: x.Provider, Link: x.Link, HostName: x.StaffName,
		Cancelled: x.Status == domain.SessionCancelled}
}

// TutoringLiveTarget: the session's tutor (and dirección / coordinación) host it; the students called join
func (s *WellbeingService) TutoringLiveTarget(ctx context.Context, token, slug, id string) (domain.LiveTarget, error) {
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return domain.LiveTarget{}, err
	}
	x, err := s.tutoring.Session(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.LiveTarget{}, err
	}
	if x.Mode != "virtual" {
		return domain.LiveTarget{}, domain.ErrLiveNotVirtual
	}
	t := tutoringTarget(sc, x)
	switch me := sc.ownStudent(); {
	case sc.staffID != "" && x.StaffID == sc.staffID, sc.office():
		t.IsHost = true
	case me != "" && slices.ContainsFunc(x.Attendees, func(a domain.SessionAttendee) bool { return a.StudentID == me }):
		t.StudentID = me
	default:
		return domain.LiveTarget{}, domain.ErrLiveForbidden
	}
	return t, nil
}

// TutoringLiveAgenda: the tutor's virtual sessions, or the student's own
func (s *WellbeingService) TutoringLiveAgenda(ctx context.Context, token, slug string, from, to time.Time) ([]domain.LiveTarget, error) {
	if s.tutoring == nil {
		return []domain.LiveTarget{}, nil
	}
	sc, _, err := s.tutoringScope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	f := domain.SessionFilter{InstitutionID: sc.institution.ID, From: &from, To: &to}
	host := false
	switch me := sc.ownStudent(); {
	case sc.staffID != "" && !sc.family():
		f.StaffID, host = sc.staffID, true
	case me != "":
		f.StudentID = me
	default:
		return []domain.LiveTarget{}, nil
	}
	list, err := s.tutoring.Sessions(ctx, f)
	if err != nil {
		return nil, err
	}
	out := []domain.LiveTarget{}
	for _, x := range list {
		if x.Mode != "virtual" || x.Status == domain.SessionCancelled {
			continue
		}
		t := tutoringTarget(sc, x)
		t.IsHost = host
		out = append(out, t)
	}
	return out, nil
}

// ---------- psychology appointments ----------

func psychTarget(sc wellScope, a domain.PsychAppointment) domain.LiveTarget {
	return domain.LiveTarget{InstitutionID: sc.institution.ID, Source: domain.SourcePsychology, ID: a.ID, Title: "Cita con psicología", Context: "Psicología",
		StartsAt: a.ScheduledAt, DurationMin: a.DurationMin, Provider: a.Provider, Link: a.Link, HostName: a.CreatedName,
		Cancelled: a.Status == domain.SessionCancelled}
}

func (s *WellbeingService) meetingsScope(ctx context.Context, token, slug string) (wellScope, error) {
	if s.meetings == nil {
		return wellScope{}, domain.ErrNotFound
	}
	return s.scope(ctx, token, slug)
}

// PsychLiveTarget: psychology hosts; the student of the case joins. Nobody else learns the appointment exists
func (s *WellbeingService) PsychLiveTarget(ctx context.Context, token, slug, id string) (domain.LiveTarget, error) {
	sc, err := s.meetingsScope(ctx, token, slug)
	if err != nil {
		return domain.LiveTarget{}, err
	}
	a, err := s.meetings.Appointment(ctx, sc.institution.ID, id)
	if err != nil {
		return domain.LiveTarget{}, err
	}
	t := psychTarget(sc, a)
	switch {
	case sc.psychologist():
		t.IsHost = true
	case sc.ownStudent() != "" && sc.ownStudent() == a.StudentID:
		t.StudentID = a.StudentID
	default:
		return domain.LiveTarget{}, domain.ErrNotFound
	}
	if a.Mode != "virtual" {
		return domain.LiveTarget{}, domain.ErrLiveNotVirtual
	}
	return t, nil
}

// PsychLiveAgenda: every virtual appointment for psychology, the student's own for a student
func (s *WellbeingService) PsychLiveAgenda(ctx context.Context, token, slug string, from, to time.Time) ([]domain.LiveTarget, error) {
	if s.meetings == nil {
		return []domain.LiveTarget{}, nil
	}
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	student := ""
	switch {
	case sc.psychologist():
	case sc.ownStudent() != "":
		student = sc.ownStudent()
	default:
		return []domain.LiveTarget{}, nil
	}
	list, err := s.meetings.Appointments(ctx, sc.institution.ID, "", student, &from, &to)
	if err != nil {
		return nil, err
	}
	out := []domain.LiveTarget{}
	for i := len(list) - 1; i >= 0; i-- {
		a := list[i]
		if a.Mode != "virtual" || a.Status == domain.SessionCancelled {
			continue
		}
		t := psychTarget(sc, a)
		t.IsHost = sc.psychologist()
		if t.IsHost {
			t.Context = "Psicología · " + a.StudentName
		}
		out = append(out, t)
	}
	return out, nil
}

// ValidateAppointment normalizes and checks an appointment; Spanish messages by field
func ValidateAppointment(a *domain.PsychAppointment) map[string]string {
	errs := map[string]string{}
	a.Link = strings.TrimSpace(a.Link)
	if a.Mode == "" {
		a.Mode = "virtual"
	}
	if a.Provider == "" {
		a.Provider = domain.ProviderPlatform
	}
	if !appointmentModes[a.Mode] {
		errs["mode"] = "Elige virtual o presencial."
	}
	if !domain.ValidProvider(a.Provider) {
		errs["provider"] = "Elige dónde será la cita: en la plataforma o en Google Meet."
	}
	if a.Mode != "virtual" || a.Provider == domain.ProviderPlatform {
		a.Link = ""
	}
	if a.Link != "" && (!httpsPattern.MatchString(a.Link) || len(a.Link) > 500) {
		errs["link"] = "Escribe una dirección completa que empiece con https://."
	}
	if a.ScheduledAt.IsZero() {
		errs["scheduledAt"] = "Indica la fecha y la hora."
	}
	if a.DurationMin < 5 || a.DurationMin > 240 {
		errs["durationMin"] = "La duración va de 5 a 240 minutos."
	}
	return errs
}

// SavePsychAppointment schedules (or reschedules) an appointment with the student of a case; psychology only
func (s *WellbeingService) SavePsychAppointment(ctx context.Context, token, slug string, in domain.PsychAppointment) (domain.PsychAppointment, error) {
	sc, err := s.meetingsScope(ctx, token, slug)
	if err != nil {
		return domain.PsychAppointment{}, err
	}
	if in.ID != "" {
		old, err := s.meetings.Appointment(ctx, sc.institution.ID, in.ID)
		if err != nil {
			return old, err
		}
		in.CaseID = old.CaseID
	}
	if _, err := s.psychCase(ctx, sc, in.CaseID); err != nil {
		return domain.PsychAppointment{}, err
	}
	if errs := ValidateAppointment(&in); len(errs) > 0 {
		return domain.PsychAppointment{}, domain.Validation(errs)
	}
	in.HostUserID, in.CreatedName = sc.userID, sc.name()
	out, err := s.meetings.SaveAppointment(ctx, sc.institution.ID, in)
	if err != nil {
		return out, err
	}
	s.tutoringChanged(ctx, sc, "psych", "")
	return out, nil
}

// SetPsychAppointmentStatus marks an appointment held, missed or cancelled (or back to scheduled)
func (s *WellbeingService) SetPsychAppointmentStatus(ctx context.Context, token, slug, id, status string) (domain.PsychAppointment, error) {
	sc, err := s.meetingsScope(ctx, token, slug)
	if err != nil {
		return domain.PsychAppointment{}, err
	}
	if !sc.psychologist() {
		return domain.PsychAppointment{}, domain.ErrPsychConfidential
	}
	if !appointmentStatuses[status] {
		return domain.PsychAppointment{}, domain.Validation(map[string]string{"status": "Estado desconocido."})
	}
	out, err := s.meetings.SetAppointmentStatus(ctx, sc.institution.ID, id, status)
	if err == nil {
		s.tutoringChanged(ctx, sc, "psych", "")
	}
	return out, err
}

// PsychAppointments lists the appointments of a case; psychology only
func (s *WellbeingService) PsychAppointments(ctx context.Context, token, slug, caseID string) ([]domain.PsychAppointment, error) {
	sc, err := s.meetingsScope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if _, err := s.psychCase(ctx, sc, caseID); err != nil {
		return nil, err
	}
	return s.meetings.Appointments(ctx, sc.institution.ID, caseID, "", nil, nil)
}
