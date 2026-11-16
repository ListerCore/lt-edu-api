package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/documentsv1"
	"api/internal/adapter/inbound/grpc/gen/participationv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// participationServer exposes service.ParticipationService (participation and permanence, phase E5)
type participationServer struct {
	participationv1.UnimplementedParticipationServiceServer
	participation *service.ParticipationService
	logger        zerolog.Logger
}

func (s *participationServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("participation grpc call failed")
	}
	return fromDomain(err)
}

func toRule(r domain.PermanenceRule) *participationv1.PermanenceRule {
	return &participationv1.PermanenceRule{WindowDays: int32(r.WindowDays), MinActiveDays: int32(r.MinActiveDays), InactiveDays: int32(r.InactiveDays),
		LowPercent: int32(r.LowPercent), UpdatedByName: r.UpdatedByName, UpdatedAt: rfc3339(r.UpdatedAt)}
}

func (s *participationServer) GetPermanenceBoard(ctx context.Context, req *participationv1.GetPermanenceBoardRequest) (*participationv1.GetPermanenceBoardResponse, error) {
	b, err := s.participation.Board(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("GetPermanenceBoard", err)
	}
	out := &participationv1.GetPermanenceBoardResponse{Rule: toRule(b.Rule), PeriodId: b.PeriodID, CanEditRule: b.CanEditRule}
	for _, x := range b.Sections {
		out.Sections = append(out.Sections, &participationv1.DistanceSection{Id: x.ID, Name: x.Name, LevelName: x.LevelName, GradeName: x.GradeName, Variant: x.Variant,
			TutorName: x.TutorName, Students: int32(x.Students)})
	}
	return out, nil
}

func (s *participationServer) SavePermanenceRule(ctx context.Context, req *participationv1.SavePermanenceRuleRequest) (*participationv1.SavePermanenceRuleResponse, error) {
	in := req.GetRule()
	r, err := s.participation.SaveRule(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.PermanenceRule{WindowDays: int(in.GetWindowDays()), MinActiveDays: int(in.GetMinActiveDays()),
		InactiveDays: int(in.GetInactiveDays()), LowPercent: int(in.GetLowPercent())})
	if err != nil {
		return nil, s.fail("SavePermanenceRule", err)
	}
	return &participationv1.SavePermanenceRuleResponse{Rule: toRule(r)}, nil
}

func (s *participationServer) GetSectionPermanence(ctx context.Context, req *participationv1.GetSectionPermanenceRequest) (*participationv1.GetSectionPermanenceResponse, error) {
	rep, err := s.participation.Section(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId())
	if err != nil {
		return nil, s.fail("GetSectionPermanence", err)
	}
	out := &participationv1.GetSectionPermanenceResponse{Label: rep.Label, PeriodName: rep.PeriodName, Rule: toRule(rep.Rule), CanAdjust: rep.CanAdjust,
		From: formatDate(rep.From), To: formatDate(rep.To)}
	for _, r := range rep.Students {
		a := r.Activity
		row := &participationv1.StudentPermanence{EnrollmentId: r.Student.EnrollmentID, StudentId: r.Student.StudentID, StudentName: r.Student.StudentName,
			StudentCode: r.Student.StudentCode, Status: r.Student.Status, Present: int32(r.Present), Counted: int32(r.Counted), Percent: int32(r.Percent), IdleDays: int32(r.IdleDays),
			Activity: &participationv1.Activity{ActiveDays: int32(len(a.Days)), Logins: int32(a.Logins), Views: int32(a.Views), Live: int32(a.Live), Attempts: int32(a.Attempts),
				Submissions: int32(a.Submissions), Posts: int32(a.Posts), Tutoring: int32(a.Tutoring), LastAt: rfc3339(a.LastAt)}}
		for _, w := range r.Windows {
			row.Windows = append(row.Windows, &participationv1.PermanenceWindow{Start: formatDate(w.Start), End: formatDate(w.End), ActiveDays: int32(w.ActiveDays),
				Present: w.Present, Adjusted: w.Adjusted, Reason: w.Reason})
		}
		out.Students = append(out.Students, row)
	}
	return out, nil
}

func (s *participationServer) AdjustPermanence(ctx context.Context, req *participationv1.AdjustPermanenceRequest) (*participationv1.AdjustPermanenceResponse, error) {
	start, err := requiredDate("windowStart", req.GetWindowStart())
	if err != nil {
		return nil, s.fail("AdjustPermanence", err)
	}
	if err := s.participation.Adjust(ctx, bearer(ctx), req.GetInstitutionSlug(), service.AdjustInput{EnrollmentID: req.GetEnrollmentId(), WindowStart: start,
		Present: req.GetPresent(), Clear: req.GetClear(), Reason: req.GetReason()}); err != nil {
		return nil, s.fail("AdjustPermanence", err)
	}
	return &participationv1.AdjustPermanenceResponse{}, nil
}

func (s *participationServer) GetRoomParticipation(ctx context.Context, req *participationv1.GetRoomParticipationRequest) (*participationv1.GetRoomParticipationResponse, error) {
	rows, err := s.participation.RoomParticipation(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetRoomId())
	if err != nil {
		return nil, s.fail("GetRoomParticipation", err)
	}
	out := &participationv1.GetRoomParticipationResponse{}
	for _, r := range rows {
		out.Students = append(out.Students, &participationv1.RoomParticipation{StudentId: r.StudentID, StudentName: r.StudentName, StudentCode: r.StudentCode,
			ItemsSeen: int32(r.ItemsSeen), Views: int32(r.Views), Live: int32(r.Live), Attempts: int32(r.Attempts), Submissions: int32(r.Submissions), Posts: int32(r.Posts),
			ActiveDays: int32(r.ActiveDays), LastAt: rfc3339(r.LastAt)})
	}
	return out, nil
}

// ---------- documents ----------

func (s *documentsServer) PermanencePdf(ctx context.Context, req *documentsv1.PermanencePdfRequest) (*documentsv1.PermanencePdfResponse, error) {
	f, err := s.documents.PermanencePdf(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId())
	file, err := s.reply("PermanencePdf", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.PermanencePdfResponse{File: file}, nil
}

func (s *documentsServer) PermanenceXlsx(ctx context.Context, req *documentsv1.PermanenceXlsxRequest) (*documentsv1.PermanenceXlsxResponse, error) {
	f, err := s.documents.PermanenceXlsx(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId())
	file, err := s.reply("PermanenceXlsx", f, err)
	if err != nil {
		return nil, err
	}
	return &documentsv1.PermanenceXlsxResponse{File: file}, nil
}
