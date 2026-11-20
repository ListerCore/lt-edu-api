package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/assessmentv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// closingServer exposes service.ClosingService to lt-edu-app's server
type closingServer struct {
	assessmentv1.UnimplementedClosingServiceServer
	closing *service.ClosingService
	logger  zerolog.Logger
}

func (s *closingServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("closing grpc call failed")
	}
	return fromDomain(err)
}

func toRecordInfo(r domain.Record) *assessmentv1.RecordInfo {
	return &assessmentv1.RecordInfo{Id: r.ID, SectionId: r.SectionID, SectionLabel: r.SectionLabel, AssignmentId: r.AssignmentID, Kind: r.Kind, Number: int32(r.Number), Code: r.Code,
		Status: r.Status, IssuedName: r.IssuedName, IssuedAt: instant(&r.IssuedAt), ActVersionId: r.ActVersionID, ActStatus: r.ActStatus}
}

func toRecordInfos(items []domain.Record) []*assessmentv1.RecordInfo {
	out := make([]*assessmentv1.RecordInfo, len(items))
	for i, r := range items {
		out[i] = toRecordInfo(r)
	}
	return out
}

func toCounts(m map[string]int) map[string]int32 {
	out := make(map[string]int32, len(m))
	for k, v := range m {
		out[k] = int32(v)
	}
	return out
}

func toSectionClosing(r service.SectionResult) *assessmentv1.SectionClosing {
	out := &assessmentv1.SectionClosing{SectionId: r.Section.ID, SectionLabel: r.Section.Label, LevelName: r.Section.LevelName, TutorName: r.Section.TutorName, PeriodId: r.Period.ID,
		PeriodName: r.Period.Name, Rule: r.Rule.Name, Referential: r.Rule.Referential, Automatic: r.Rule.Automatic, LastTermClosed: r.LastTermClosed, CanManage: r.CanManage,
		Records: toRecordInfos(r.Records)}
	for _, a := range r.Areas {
		area := &assessmentv1.ClosingArea{SubjectId: a.SubjectID, SubjectCode: a.SubjectCode, Name: a.Name}
		for _, c := range a.Competencies {
			area.Competencies = append(area.Competencies, &assessmentv1.Competency{Id: c.ID, Code: c.Code, Name: c.Name})
		}
		out.Areas = append(out.Areas, area)
	}
	for _, x := range r.Rows {
		out.Rows = append(out.Rows, &assessmentv1.ClosingRow{Student: &assessmentv1.StudentRef{Id: x.Student.ID, Code: x.Student.Code, Name: x.Student.Name, Document: x.Student.Document},
			Final: x.Final, Computed: x.Computed, Reason: x.Reason, Decided: x.Decided, DecisionNote: x.DecisionNote, DecidedName: x.DecidedName, Situation: x.Situation,
			Confirmed: x.Confirmed, Targets: x.Targets, Recovery: x.Recovery, AfterRecovery: x.AfterRecovery, EnrollmentId: x.EnrollmentID, GradeShort: x.GradeShort,
			ReligionExempt: x.ReligionExempt, SiagieStatus: x.SiagieStatus, SiagieNote: x.SiagieNote, SiagieAt: instant(x.SiagieAt)})
	}
	return out
}

func toUnitClosing(u service.UnitResultSheet) *assessmentv1.UnitClosing {
	out := &assessmentv1.UnitClosing{AssignmentId: u.Assignment.ID, SubjectName: u.Assignment.SubjectName, StaffName: u.Assignment.StaffName, SectionId: u.Section.ID,
		SectionLabel: u.Section.Label, LevelName: u.Section.LevelName, PeriodName: u.Period.Name, PassingGrade: int32(u.Passing), AbsenceLimit: int32(u.Limit),
		CanManage: u.CanManage, Records: toRecordInfos(u.Records)}
	for _, e := range u.Evaluations {
		out.Evaluations = append(out.Evaluations, &assessmentv1.Evaluation{Id: e.ID, Name: e.Name, Weight: int32(e.Weight), HeldOn: datePtr(e.HeldOn)})
	}
	for _, x := range u.Rows {
		scores := map[string]int32{}
		for k, v := range x.Scores {
			scores[k] = int32(v)
		}
		out.Rows = append(out.Rows, &assessmentv1.UnitRow{Student: &assessmentv1.StudentRef{Id: x.Student.ID, Code: x.Student.Code, Name: x.Student.Name, Document: x.Student.Document},
			Scores: scores, Average: x.Average, Grade: int32Ptr(x.Grade), Missing: int32(x.Missing), AbsentPercent: x.AbsentPercent, Status: x.Status,
			RecoveryScore: int32Ptr(x.RecoveryScore), FinalGrade: int32Ptr(x.FinalGrade), Confirmed: x.Confirmed})
	}
	return out
}

func (s *closingServer) GetClosing(ctx context.Context, req *assessmentv1.GetClosingRequest) (*assessmentv1.GetClosingResponse, error) {
	o, err := s.closing.Overview(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("GetClosing", err)
	}
	out := &assessmentv1.GetClosingResponse{Mode: o.Mode, Periods: toPeriods(o.Periods), LastTermClosed: o.LastTermClosed, CanManage: o.CanManage, Records: toRecordInfos(o.Records)}
	if o.Period.ID != "" {
		out.Period = toPeriod(o.Period)
	}
	for _, x := range o.Sections {
		out.Sections = append(out.Sections, &assessmentv1.SectionClosingSummary{SectionId: x.SectionID, Label: x.Label, Students: int32(x.Students), Counts: toCounts(x.Counts),
			Confirmed: int32(x.Confirmed), Recovered: int32(x.Recovered)})
	}
	for _, x := range o.Units {
		out.Units = append(out.Units, &assessmentv1.UnitClosingSummary{AssignmentId: x.AssignmentID, SectionId: x.SectionID, SectionLabel: x.SectionLabel, SubjectName: x.SubjectName,
			StaffName: x.StaffName, Students: int32(x.Students), Confirmed: x.Confirmed, Counts: toCounts(x.Counts)})
	}
	return out, nil
}

func (s *closingServer) GetSectionClosing(ctx context.Context, req *assessmentv1.GetSectionClosingRequest) (*assessmentv1.GetSectionClosingResponse, error) {
	r, err := s.closing.Section(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId())
	if err != nil {
		return nil, s.fail("GetSectionClosing", err)
	}
	return &assessmentv1.GetSectionClosingResponse{Closing: toSectionClosing(r)}, nil
}

func (s *closingServer) DecideSituation(ctx context.Context, req *assessmentv1.DecideSituationRequest) (*assessmentv1.DecideSituationResponse, error) {
	r, err := s.closing.Decide(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetStudentId(), req.GetSituation(), req.GetNote())
	if err != nil {
		return nil, s.fail("DecideSituation", err)
	}
	return &assessmentv1.DecideSituationResponse{Closing: toSectionClosing(r)}, nil
}

func (s *closingServer) ConfirmSection(ctx context.Context, req *assessmentv1.ConfirmSectionRequest) (*assessmentv1.ConfirmSectionResponse, error) {
	r, err := s.closing.Confirm(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId())
	if err != nil {
		return nil, s.fail("ConfirmSection", err)
	}
	return &assessmentv1.ConfirmSectionResponse{Closing: toSectionClosing(r)}, nil
}

func (s *closingServer) SaveRecovery(ctx context.Context, req *assessmentv1.SaveRecoveryRequest) (*assessmentv1.SaveRecoveryResponse, error) {
	r, err := s.closing.SaveRecovery(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetStudentId(), req.GetLevels())
	if err != nil {
		return nil, s.fail("SaveRecovery", err)
	}
	return &assessmentv1.SaveRecoveryResponse{Closing: toSectionClosing(r)}, nil
}

func (s *closingServer) GetUnitClosing(ctx context.Context, req *assessmentv1.GetUnitClosingRequest) (*assessmentv1.GetUnitClosingResponse, error) {
	u, err := s.closing.Unit(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId())
	if err != nil {
		return nil, s.fail("GetUnitClosing", err)
	}
	return &assessmentv1.GetUnitClosingResponse{Closing: toUnitClosing(u)}, nil
}

func (s *closingServer) ConfirmUnit(ctx context.Context, req *assessmentv1.ConfirmUnitRequest) (*assessmentv1.ConfirmUnitResponse, error) {
	u, err := s.closing.ConfirmUnit(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId())
	if err != nil {
		return nil, s.fail("ConfirmUnit", err)
	}
	return &assessmentv1.ConfirmUnitResponse{Closing: toUnitClosing(u)}, nil
}

func (s *closingServer) SaveUnitRecovery(ctx context.Context, req *assessmentv1.SaveUnitRecoveryRequest) (*assessmentv1.SaveUnitRecoveryResponse, error) {
	u, err := s.closing.SaveUnitRecovery(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetStudentId(), intPtr(req.Score))
	if err != nil {
		return nil, s.fail("SaveUnitRecovery", err)
	}
	return &assessmentv1.SaveUnitRecoveryResponse{Closing: toUnitClosing(u)}, nil
}

func (s *closingServer) IssueRecord(ctx context.Context, req *assessmentv1.IssueRecordRequest) (*assessmentv1.IssueRecordResponse, error) {
	r, err := s.closing.Issue(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetAssignmentId(), req.GetKind())
	if err != nil {
		return nil, s.fail("IssueRecord", err)
	}
	return &assessmentv1.IssueRecordResponse{Record: toRecordInfo(r)}, nil
}

func (s *closingServer) GetRecord(ctx context.Context, req *assessmentv1.GetRecordRequest) (*assessmentv1.GetRecordResponse, error) {
	r, err := s.closing.Record(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetRecord", err)
	}
	return &assessmentv1.GetRecordResponse{Record: toRecordInfo(r), Content: string(r.Content)}, nil
}

func (s *closingServer) ExportResults(ctx context.Context, req *assessmentv1.ExportResultsRequest) (*assessmentv1.ExportResultsResponse, error) {
	name, body, err := s.closing.Export(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetAssignmentId())
	if err != nil {
		return nil, s.fail("ExportResults", err)
	}
	return &assessmentv1.ExportResultsResponse{FileName: name, Content: body}, nil
}
