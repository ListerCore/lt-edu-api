package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/meetingsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// meetingsServer exposes service.MeetingService (clases en vivo) to lt-edu-app's server
type meetingsServer struct {
	meetingsv1.UnimplementedMeetingsServiceServer
	meetings  *service.MeetingService
	wellbeing *service.WellbeingService
	logger    zerolog.Logger
}

func (s *meetingsServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("meetings grpc call failed")
	}
	return fromDomain(err)
}

func toLiveSession(t domain.LiveTarget) *meetingsv1.Session {
	return &meetingsv1.Session{Source: t.Source, Id: t.ID, Title: t.Title, Context: t.Context, StartsAt: instant(&t.StartsAt), DurationMin: int32(t.DurationMin),
		Provider: t.Provider, HostName: t.HostName, IsHost: t.IsHost}
}

func toAppointment(a domain.PsychAppointment) *meetingsv1.Appointment {
	return &meetingsv1.Appointment{Id: a.ID, CaseId: a.CaseID, StudentId: a.StudentID, StudentName: a.StudentName, ScheduledAt: instant(&a.ScheduledAt),
		DurationMin: int32(a.DurationMin), Mode: a.Mode, Provider: a.Provider, Link: a.Link, Status: a.Status, CreatedName: a.CreatedName}
}

func (s *meetingsServer) GetLiveConfig(_ context.Context, _ *meetingsv1.GetLiveConfigRequest) (*meetingsv1.GetLiveConfigResponse, error) {
	return &meetingsv1.GetLiveConfigResponse{PlatformAvailable: s.meetings.PlatformAvailable()}, nil
}

func (s *meetingsServer) GetAgenda(ctx context.Context, req *meetingsv1.GetAgendaRequest) (*meetingsv1.GetAgendaResponse, error) {
	from, err := parseInstant("from", req.GetFrom())
	if err != nil {
		return nil, s.fail("GetAgenda", err)
	}
	to, err := parseInstant("to", req.GetTo())
	if err != nil {
		return nil, s.fail("GetAgenda", err)
	}
	if from.IsZero() {
		// Sessions that started in the last hours are still open to join
		from = time.Now().Add(-6 * time.Hour)
	}
	if to.IsZero() {
		to = from.AddDate(0, 0, 14)
	}
	list, err := s.meetings.Agenda(ctx, bearer(ctx), req.GetInstitutionSlug(), from, to)
	if err != nil {
		return nil, s.fail("GetAgenda", err)
	}
	out := &meetingsv1.GetAgendaResponse{PlatformAvailable: s.meetings.PlatformAvailable(), Sessions: []*meetingsv1.Session{}}
	for _, t := range list {
		out.Sessions = append(out.Sessions, toLiveSession(t))
	}
	return out, nil
}

func (s *meetingsServer) JoinSession(ctx context.Context, req *meetingsv1.JoinSessionRequest) (*meetingsv1.JoinSessionResponse, error) {
	j, err := s.meetings.Join(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSource(), req.GetId())
	if err != nil {
		return nil, s.fail("JoinSession", err)
	}
	return &meetingsv1.JoinSessionResponse{Session: toLiveSession(j.Target), MeetUrl: j.MeetURL, ServerUrl: j.ServerURL, Token: j.Token, RoomName: j.RoomName,
		Identity: j.Identity, DisplayName: j.Name}, nil
}

func (s *meetingsServer) GetAttendance(ctx context.Context, req *meetingsv1.GetAttendanceRequest) (*meetingsv1.GetAttendanceResponse, error) {
	list, err := s.meetings.Attendance(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSource(), req.GetId())
	if err != nil {
		return nil, s.fail("GetAttendance", err)
	}
	out := &meetingsv1.GetAttendanceResponse{Attendees: []*meetingsv1.Attendee{}}
	for _, j := range list {
		out.Attendees = append(out.Attendees, &meetingsv1.Attendee{UserId: j.UserID, StudentId: j.StudentID, DisplayName: j.DisplayName,
			FirstAt: instant(&j.FirstAt), LastAt: instant(&j.LastAt), Joins: int32(j.Joins)})
	}
	return out, nil
}

func (s *meetingsServer) ListAppointments(ctx context.Context, req *meetingsv1.ListAppointmentsRequest) (*meetingsv1.ListAppointmentsResponse, error) {
	list, err := s.wellbeing.PsychAppointments(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetCaseId())
	if err != nil {
		return nil, s.fail("ListAppointments", err)
	}
	out := &meetingsv1.ListAppointmentsResponse{PlatformAvailable: s.meetings.PlatformAvailable(), Appointments: []*meetingsv1.Appointment{}}
	for _, a := range list {
		out.Appointments = append(out.Appointments, toAppointment(a))
	}
	return out, nil
}

func (s *meetingsServer) SaveAppointment(ctx context.Context, req *meetingsv1.SaveAppointmentRequest) (*meetingsv1.SaveAppointmentResponse, error) {
	at, err := parseInstant("scheduledAt", req.GetScheduledAt())
	if err != nil {
		return nil, s.fail("SaveAppointment", err)
	}
	a, err := s.wellbeing.SavePsychAppointment(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.PsychAppointment{ID: req.GetId(), CaseID: req.GetCaseId(),
		ScheduledAt: at, DurationMin: int(req.GetDurationMin()), Mode: req.GetMode(), Provider: req.GetProvider(), Link: req.GetLink()})
	if err != nil {
		return nil, s.fail("SaveAppointment", err)
	}
	return &meetingsv1.SaveAppointmentResponse{Appointment: toAppointment(a)}, nil
}

func (s *meetingsServer) SetAppointmentStatus(ctx context.Context, req *meetingsv1.SetAppointmentStatusRequest) (*meetingsv1.SetAppointmentStatusResponse, error) {
	a, err := s.wellbeing.SetPsychAppointmentStatus(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus())
	if err != nil {
		return nil, s.fail("SetAppointmentStatus", err)
	}
	return &meetingsv1.SetAppointmentStatusResponse{Appointment: toAppointment(a)}, nil
}
