package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/wellbeingv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// tutoringServer exposes the virtual tutoring of service.WellbeingService (phase E6) to joseph-app's server
type tutoringServer struct {
	wellbeingv1.UnimplementedTutoringServiceServer
	wellbeing *service.WellbeingService
	logger    zerolog.Logger
}

func (s *tutoringServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("tutoring grpc call failed")
	}
	return fromDomain(err)
}

func toTutorStudent(x domain.TutorStudent) *wellbeingv1.TutorStudent {
	return &wellbeingv1.TutorStudent{StudentId: x.StudentID, Code: x.Code, Name: x.Name, Placement: x.Placement, SectionId: x.SectionID, AttentionMode: x.AttentionMode,
		StaffId: x.StaffID, StaffName: x.StaffName, Individual: x.Individual, LastAt: instant(x.LastAt)}
}

func toTutorLoads(items []domain.TutorLoad) []*wellbeingv1.TutorLoad {
	out := make([]*wellbeingv1.TutorLoad, 0, len(items))
	for _, x := range items {
		out = append(out, &wellbeingv1.TutorLoad{StaffId: x.StaffID, Name: x.Name, Sections: x.Sections, BySection: int32(x.BySection), Individual: int32(x.Individual)})
	}
	return out
}

func toTutoringSession(x domain.TutoringSession) *wellbeingv1.TutoringSession {
	out := &wellbeingv1.TutoringSession{Id: x.ID, StaffId: x.StaffID, StaffName: x.StaffName, Kind: x.Kind, Mode: x.Mode, ScheduledAt: instant(&x.ScheduledAt),
		DurationMin: int32(x.DurationMin), Link: x.Link, Reason: x.Reason, AlertKind: x.AlertKind, Status: x.Status, Notes: x.Notes, Agreements: x.Agreements,
		HeldAt: instant(x.HeldAt), CreatedName: x.CreatedName}
	for _, a := range x.Attendees {
		out.Attendees = append(out.Attendees, &wellbeingv1.SessionAttendee{StudentId: a.StudentID, StudentName: a.StudentName, Placement: a.Placement, Attended: a.Attended,
			InterviewId: a.InterviewID})
	}
	return out
}

func toTutoringSessions(items []domain.TutoringSession) []*wellbeingv1.TutoringSession {
	out := make([]*wellbeingv1.TutoringSession, 0, len(items))
	for _, x := range items {
		out = append(out, toTutoringSession(x))
	}
	return out
}

func toThread(t domain.Thread) *wellbeingv1.Thread {
	return &wellbeingv1.Thread{Id: t.ID, StudentId: t.StudentID, StudentName: t.StudentName, Placement: t.Placement, StaffId: t.StaffID, StaffName: t.StaffName,
		LastMessageAt: instant(t.LastMessageAt), LastBody: t.LastBody, LastSide: t.LastSide, Unread: int32(t.Unread)}
}

func toThreads(items []domain.Thread) []*wellbeingv1.Thread {
	out := make([]*wellbeingv1.Thread, 0, len(items))
	for _, t := range items {
		out = append(out, toThread(t))
	}
	return out
}

func toMessage(m domain.Message) *wellbeingv1.TutoringMessage {
	return &wellbeingv1.TutoringMessage{Id: m.ID, ThreadId: m.ThreadID, Side: m.Side, AuthorName: m.AuthorName, Body: m.Body, CreatedAt: instant(&m.CreatedAt)}
}

func toThreadView(v service.ThreadView) *wellbeingv1.ThreadView {
	out := &wellbeingv1.ThreadView{Thread: toThread(v.Thread), Side: v.Side}
	for _, m := range v.Messages {
		out.Messages = append(out.Messages, toMessage(m))
	}
	return out
}

func toSaanee(x domain.SaaneeReferral) *wellbeingv1.SaaneeReferral {
	return &wellbeingv1.SaaneeReferral{Id: x.ID, StudentId: x.StudentID, StudentName: x.StudentName, Placement: x.Placement, Reason: x.Reason, Status: x.Status,
		SentOn: formatDate(x.SentOn), Reference: x.Reference, Response: x.Response, ReferredName: x.ReferredName, UpdatedName: x.UpdatedName, UpdatedAt: instant(&x.UpdatedAt)}
}

func toTutorAssignments(a service.TutorAssignments) *wellbeingv1.GetTutorAssignmentsResponse {
	out := &wellbeingv1.GetTutorAssignmentsResponse{Loads: toTutorLoads(a.Loads)}
	for _, c := range a.Candidates {
		out.Candidates = append(out.Candidates, &wellbeingv1.TutorCandidate{StaffId: c.StaffID, Name: c.Name, Position: c.Position})
	}
	for _, x := range a.Students {
		out.Students = append(out.Students, toTutorStudent(x))
	}
	return out
}

func (s *tutoringServer) GetTutorBoard(ctx context.Context, req *wellbeingv1.GetTutorBoardRequest) (*wellbeingv1.GetTutorBoardResponse, error) {
	b, err := s.wellbeing.TutorBoard(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStaffId())
	if err != nil {
		return nil, s.fail("GetTutorBoard", err)
	}
	out := &wellbeingv1.GetTutorBoardResponse{StaffId: b.StaffID, StaffName: b.StaffName, Tutors: toTutorLoads(b.Tutors), Sessions: toTutoringSessions(b.Sessions),
		Threads: toThreads(b.Threads), CanAssign: b.CanAssign, CanMessage: b.CanMessage, Today: formatDate(b.Today)}
	for _, r := range b.Rows {
		out.Rows = append(out.Rows, &wellbeingv1.TutorBoardRow{Student: toTutorStudent(r.TutorStudent), IdleDays: int32(r.IdleDays), Alerts: r.Alerts, Overdue: int32(r.Overdue),
			DueSoon: int32(r.DueSoon), Unread: int32(r.Unread), NextSession: instant(r.NextSession), Saanee: r.Saanee})
	}
	for _, a := range b.Alerts {
		out.Alerts = append(out.Alerts, &wellbeingv1.TutorBoardAlert{Kind: a.Kind, Code: a.Code, Level: a.Level, StudentId: a.StudentID, StudentName: a.StudentName,
			Placement: a.Placement, Title: a.Title, Detail: a.Detail, Since: instant(a.Since)})
	}
	for _, x := range b.Saanee {
		out.Saanee = append(out.Saanee, toSaanee(x))
	}
	for _, w := range b.Pending {
		out.Pending = append(out.Pending, &wellbeingv1.PendingWork{StudentId: w.StudentID, ItemId: w.ItemID, RoomId: w.RoomID, Title: w.Title, Subject: w.Subject,
			ClosesAt: instant(&w.ClosesAt), Overdue: w.Overdue})
	}
	return out, nil
}

func (s *tutoringServer) GetTutorAssignments(ctx context.Context, req *wellbeingv1.GetTutorAssignmentsRequest) (*wellbeingv1.GetTutorAssignmentsResponse, error) {
	a, err := s.wellbeing.TutorAssignments(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetTutorAssignments", err)
	}
	return toTutorAssignments(a), nil
}

func (s *tutoringServer) AssignTutor(ctx context.Context, req *wellbeingv1.AssignTutorRequest) (*wellbeingv1.AssignTutorResponse, error) {
	a, err := s.wellbeing.AssignTutor(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentIds(), req.GetStaffId())
	if err != nil {
		return nil, s.fail("AssignTutor", err)
	}
	return &wellbeingv1.AssignTutorResponse{Assignments: toTutorAssignments(a)}, nil
}

func (s *tutoringServer) ListTutoringSessions(ctx context.Context, req *wellbeingv1.ListTutoringSessionsRequest) (*wellbeingv1.ListTutoringSessionsResponse, error) {
	dates, err := parseDates(map[string]string{"from": req.GetFrom(), "to": req.GetTo()})
	if err != nil {
		return nil, s.fail("ListTutoringSessions", err)
	}
	from, to := dates["from"], dates["to"]
	if from.IsZero() || to.IsZero() || to.Before(from) || to.Sub(from) > 120*24*time.Hour {
		return nil, s.fail("ListTutoringSessions", domain.Validation(map[string]string{"from": "Elige un rango de hasta 120 días."}))
	}
	list, err := s.wellbeing.TutoringSessions(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStaffId(), from, to.AddDate(0, 0, 1))
	if err != nil {
		return nil, s.fail("ListTutoringSessions", err)
	}
	return &wellbeingv1.ListTutoringSessionsResponse{Sessions: toTutoringSessions(list)}, nil
}

func (s *tutoringServer) SaveTutoringSession(ctx context.Context, req *wellbeingv1.SaveTutoringSessionRequest) (*wellbeingv1.SaveTutoringSessionResponse, error) {
	at, err := time.Parse(time.RFC3339, req.GetScheduledAt())
	if err != nil && req.GetScheduledAt() != "" {
		return nil, s.fail("SaveTutoringSession", domain.Validation(map[string]string{"scheduledAt": "Fecha y hora no válidas."}))
	}
	x, err := s.wellbeing.SaveTutoringSession(ctx, bearer(ctx), req.GetInstitutionSlug(), service.SessionInput{ID: req.GetId(), StaffID: req.GetStaffId(), Kind: req.GetKind(),
		Mode: req.GetMode(), ScheduledAt: at, DurationMin: int(req.GetDurationMin()), Link: req.GetLink(), Reason: req.GetReason(), AlertKind: req.GetAlertKind(),
		StudentIDs: req.GetStudentIds()})
	if err != nil {
		return nil, s.fail("SaveTutoringSession", err)
	}
	return &wellbeingv1.SaveTutoringSessionResponse{Session: toTutoringSession(x)}, nil
}

func (s *tutoringServer) HoldTutoringSession(ctx context.Context, req *wellbeingv1.HoldTutoringSessionRequest) (*wellbeingv1.HoldTutoringSessionResponse, error) {
	due, err := optionalDate("commitmentDue", req.GetCommitmentDue())
	if err != nil {
		return nil, s.fail("HoldTutoringSession", err)
	}
	x, err := s.wellbeing.HoldTutoringSession(ctx, bearer(ctx), req.GetInstitutionSlug(), service.HoldSessionInput{ID: req.GetId(), Attended: req.GetAttended(),
		Notes: req.GetNotes(), Agreements: req.GetAgreements(), Commitment: req.GetCommitment(), CommitmentDue: due})
	if err != nil {
		return nil, s.fail("HoldTutoringSession", err)
	}
	return &wellbeingv1.HoldTutoringSessionResponse{Session: toTutoringSession(x)}, nil
}

func (s *tutoringServer) SetTutoringSessionStatus(ctx context.Context, req *wellbeingv1.SetTutoringSessionStatusRequest) (*wellbeingv1.SetTutoringSessionStatusResponse, error) {
	x, err := s.wellbeing.SetTutoringSessionStatus(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus())
	if err != nil {
		return nil, s.fail("SetTutoringSessionStatus", err)
	}
	return &wellbeingv1.SetTutoringSessionStatusResponse{Session: toTutoringSession(x)}, nil
}

func (s *tutoringServer) ListThreads(ctx context.Context, req *wellbeingv1.ListThreadsRequest) (*wellbeingv1.ListThreadsResponse, error) {
	list, err := s.wellbeing.Threads(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("ListThreads", err)
	}
	return &wellbeingv1.ListThreadsResponse{Threads: toThreads(list)}, nil
}

func (s *tutoringServer) OpenThread(ctx context.Context, req *wellbeingv1.OpenThreadRequest) (*wellbeingv1.OpenThreadResponse, error) {
	v, err := s.wellbeing.OpenThread(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("OpenThread", err)
	}
	return &wellbeingv1.OpenThreadResponse{View: toThreadView(v)}, nil
}

func (s *tutoringServer) GetThread(ctx context.Context, req *wellbeingv1.GetThreadRequest) (*wellbeingv1.GetThreadResponse, error) {
	v, err := s.wellbeing.ThreadMessages(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetThreadId())
	if err != nil {
		return nil, s.fail("GetThread", err)
	}
	return &wellbeingv1.GetThreadResponse{View: toThreadView(v)}, nil
}

func (s *tutoringServer) SendMessage(ctx context.Context, req *wellbeingv1.SendMessageRequest) (*wellbeingv1.SendMessageResponse, error) {
	m, err := s.wellbeing.SendMessage(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetThreadId(), req.GetBody())
	if err != nil {
		return nil, s.fail("SendMessage", err)
	}
	return &wellbeingv1.SendMessageResponse{Message: toMessage(m)}, nil
}

func (s *tutoringServer) GetMyTutoring(ctx context.Context, req *wellbeingv1.GetMyTutoringRequest) (*wellbeingv1.GetMyTutoringResponse, error) {
	m, err := s.wellbeing.MyTutoring(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("GetMyTutoring", err)
	}
	out := &wellbeingv1.GetMyTutoringResponse{HasTutor: m.HasTutor, Sessions: toTutoringSessions(m.Sessions)}
	if m.HasTutor {
		out.Tutor = toTutorStudent(m.Tutor)
	}
	if m.Thread != nil {
		out.Thread = toThreadView(*m.Thread)
	}
	return out, nil
}

func (s *tutoringServer) ReferSaanee(ctx context.Context, req *wellbeingv1.ReferSaaneeRequest) (*wellbeingv1.ReferSaaneeResponse, error) {
	sent, err := requiredDate("sentOn", req.GetSentOn())
	if err != nil {
		return nil, s.fail("ReferSaanee", err)
	}
	x, err := s.wellbeing.ReferSaanee(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetReason(), req.GetReference(), sent)
	if err != nil {
		return nil, s.fail("ReferSaanee", err)
	}
	return &wellbeingv1.ReferSaaneeResponse{Referral: toSaanee(x)}, nil
}

func (s *tutoringServer) UpdateSaanee(ctx context.Context, req *wellbeingv1.UpdateSaaneeRequest) (*wellbeingv1.UpdateSaaneeResponse, error) {
	x, err := s.wellbeing.UpdateSaanee(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStatus(), req.GetReference(), req.GetResponse())
	if err != nil {
		return nil, s.fail("UpdateSaanee", err)
	}
	return &wellbeingv1.UpdateSaaneeResponse{Referral: toSaanee(x)}, nil
}
