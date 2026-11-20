package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/apafav1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// apafaServer exposes service.ApafaService to lt-edu-app's server
type apafaServer struct {
	apafav1.UnimplementedApafaServiceServer
	apafa  *service.ApafaService
	logger zerolog.Logger
}

func (s *apafaServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) || errors.Is(err, domain.ErrApafaNoticeFailed) {
		s.logger.Error().Err(err).Str("method", method).Msg("apafa grpc call failed")
	}
	return fromDomain(err)
}

func toMeeting(m domain.ApafaMeeting) *apafav1.Meeting {
	return &apafav1.Meeting{Id: m.ID, Title: m.Title, Kind: m.Kind, ScheduledAt: instant(&m.ScheduledAt), Modality: m.Modality, Place: m.Place, MeetingUrl: m.MeetingURL,
		Agenda: m.Agenda, Audience: m.Audience, Status: m.Status, PreviousAt: instant(m.PreviousAt), RescheduleReason: m.RescheduleReason, CancelReason: m.CancelReason,
		Minutes: m.Minutes, Agreements: m.Agreements, Attendees: int32Ptr(m.Attendees), MinutesPublishedAt: instant(m.MinutesAt), AnnouncementId: m.AnnouncementID,
		NotifiedAt: instant(m.NotifiedAt), CreatedName: m.CreatedName, CreatedAt: instant(&m.CreatedAt), UpdatedAt: instant(&m.UpdatedAt), Files: int32(m.Files)}
}

func toBoardMember(b domain.ApafaBoardMember) *apafav1.BoardMember {
	return &apafav1.BoardMember{Id: b.ID, FullName: b.FullName, Position: b.Position, Phone: b.Phone, StartsOn: formatDate(b.StartsOn), EndsOn: formatDate(b.EndsOn)}
}

// parseInstant reads an RFC 3339 instant; empty is the zero time (the service reports the missing field)
func parseInstant(field, v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, domain.Validation(map[string]string{field: "Fecha u hora no válida."})
	}
	return t, nil
}

func (s *apafaServer) GetApafa(ctx context.Context, req *apafav1.GetApafaRequest) (*apafav1.GetApafaResponse, error) {
	o, err := s.apafa.Overview(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetApafa", err)
	}
	out := &apafav1.GetApafaResponse{CanManage: o.CanManage}
	for _, m := range o.Meetings {
		out.Meetings = append(out.Meetings, toMeeting(m))
	}
	for _, b := range o.Board {
		out.Board = append(out.Board, toBoardMember(b))
	}
	if o.Fees != nil {
		out.Fees = &apafav1.Fees{Expected: int64(o.Fees.Expected), Collected: int64(o.Fees.Collected), Pending: int64(o.Fees.Pending), Charged: int32(o.Fees.Charged), Settled: int32(o.Fees.Settled)}
	}
	return out, nil
}

func (s *apafaServer) GetMeeting(ctx context.Context, req *apafav1.GetMeetingRequest) (*apafav1.GetMeetingResponse, error) {
	d, err := s.apafa.Meeting(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetMeeting", err)
	}
	return &apafav1.GetMeetingResponse{Meeting: toMeeting(d.Meeting), CanManage: d.CanManage}, nil
}

func (s *apafaServer) SaveMeeting(ctx context.Context, req *apafav1.SaveMeetingRequest) (*apafav1.SaveMeetingResponse, error) {
	m := req.GetMeeting()
	at, err := parseInstant("scheduled_at", m.GetScheduledAt())
	if err != nil {
		return nil, s.fail("SaveMeeting", err)
	}
	in := service.MeetingInput{Title: m.GetTitle(), Kind: m.GetKind(), ScheduledAt: at, Modality: m.GetModality(), Place: m.GetPlace(), MeetingURL: m.GetMeetingUrl(),
		Agenda: m.GetAgenda(), Audience: m.GetAudience()}
	saved, err := s.apafa.SaveMeeting(ctx, bearer(ctx), req.GetInstitutionSlug(), m.GetId(), in, req.GetNotify())
	if err != nil {
		return nil, s.fail("SaveMeeting", err)
	}
	return &apafav1.SaveMeetingResponse{Meeting: toMeeting(saved)}, nil
}

func (s *apafaServer) ConveneMeeting(ctx context.Context, req *apafav1.ConveneMeetingRequest) (*apafav1.ConveneMeetingResponse, error) {
	m, err := s.apafa.Convene(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("ConveneMeeting", err)
	}
	return &apafav1.ConveneMeetingResponse{Meeting: toMeeting(m)}, nil
}

func (s *apafaServer) RescheduleMeeting(ctx context.Context, req *apafav1.RescheduleMeetingRequest) (*apafav1.RescheduleMeetingResponse, error) {
	at, err := parseInstant("scheduled_at", req.GetScheduledAt())
	if err != nil {
		return nil, s.fail("RescheduleMeeting", err)
	}
	m, err := s.apafa.Reschedule(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), at, req.GetReason(), req.GetNotify())
	if err != nil {
		return nil, s.fail("RescheduleMeeting", err)
	}
	return &apafav1.RescheduleMeetingResponse{Meeting: toMeeting(m)}, nil
}

func (s *apafaServer) CancelMeeting(ctx context.Context, req *apafav1.CancelMeetingRequest) (*apafav1.CancelMeetingResponse, error) {
	m, err := s.apafa.Cancel(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetReason(), req.GetNotify())
	if err != nil {
		return nil, s.fail("CancelMeeting", err)
	}
	return &apafav1.CancelMeetingResponse{Meeting: toMeeting(m)}, nil
}

func (s *apafaServer) RecordMinutes(ctx context.Context, req *apafav1.RecordMinutesRequest) (*apafav1.RecordMinutesResponse, error) {
	in := service.MinutesInput{Minutes: req.GetMinutes(), Agreements: req.GetAgreements(), Attendees: intPtr(req.Attendees)}
	m, err := s.apafa.RecordMinutes(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), in, req.GetPublish())
	if err != nil {
		return nil, s.fail("RecordMinutes", err)
	}
	return &apafav1.RecordMinutesResponse{Meeting: toMeeting(m)}, nil
}

func (s *apafaServer) DeleteMeeting(ctx context.Context, req *apafav1.DeleteMeetingRequest) (*apafav1.DeleteMeetingResponse, error) {
	if err := s.apafa.DeleteMeeting(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteMeeting", err)
	}
	return &apafav1.DeleteMeetingResponse{}, nil
}

func (s *apafaServer) SaveBoardMember(ctx context.Context, req *apafav1.SaveBoardMemberRequest) (*apafav1.SaveBoardMemberResponse, error) {
	b := req.GetMember()
	dates, err := parseDates(map[string]string{"starts_on": b.GetStartsOn(), "ends_on": b.GetEndsOn()})
	if err != nil {
		return nil, s.fail("SaveBoardMember", err)
	}
	in := service.BoardMemberInput{FullName: b.GetFullName(), Position: b.GetPosition(), Phone: b.GetPhone(), StartsOn: dates["starts_on"], EndsOn: dates["ends_on"]}
	saved, err := s.apafa.SaveBoardMember(ctx, bearer(ctx), req.GetInstitutionSlug(), b.GetId(), in)
	if err != nil {
		return nil, s.fail("SaveBoardMember", err)
	}
	return &apafav1.SaveBoardMemberResponse{Member: toBoardMember(saved)}, nil
}

func (s *apafaServer) DeleteBoardMember(ctx context.Context, req *apafav1.DeleteBoardMemberRequest) (*apafav1.DeleteBoardMemberResponse, error) {
	if err := s.apafa.DeleteBoardMember(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeleteBoardMember", err)
	}
	return &apafav1.DeleteBoardMemberResponse{}, nil
}
