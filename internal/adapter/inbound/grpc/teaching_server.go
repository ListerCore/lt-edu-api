package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/teachingv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// staffServer and teachingServer expose service.StaffService and service.TeachingService to joseph-app's server
type staffServer struct {
	teachingv1.UnimplementedStaffServiceServer
	staff  *service.StaffService
	logger zerolog.Logger
}

type teachingServer struct {
	teachingv1.UnimplementedTeachingServiceServer
	teaching *service.TeachingService
	logger   zerolog.Logger
}

func logUnexpected(logger zerolog.Logger, method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		logger.Error().Err(err).Str("method", method).Msg("teaching grpc call failed")
	}
	return fromDomain(err)
}

func toStaff(s domain.Staff) *teachingv1.Staff {
	return &teachingv1.Staff{
		Id: s.ID, Code: s.Code, DocumentType: s.DocumentType, DocumentNumber: s.DocumentNumber, GivenNames: s.GivenNames, Surnames: s.Surnames,
		Email: s.Email, Phone: s.Phone, Position: s.Position, IsTeacher: s.IsTeacher, Specialty: s.Specialty, MaxWeeklyHours: int32Ptr(s.MaxWeeklyHours),
		HiredOn: datePtr(s.HiredOn), Status: s.Status, Notes: s.Notes, Roles: s.Roles, Invited: s.Invited, AssignedHours: int32(s.AssignedHours), TutorOf: s.TutorOf,
	}
}

func toStaffList(items []domain.Staff) []*teachingv1.Staff {
	out := make([]*teachingv1.Staff, len(items))
	for i, s := range items {
		out[i] = toStaff(s)
	}
	return out
}

func staffInput(in *teachingv1.StaffInput) (domain.StaffInput, error) {
	hired, err := optionalDate("hiredOn", in.GetHiredOn())
	if err != nil {
		return domain.StaffInput{}, err
	}
	return domain.StaffInput{
		Code: in.GetCode(), DocumentType: in.GetDocumentType(), DocumentNumber: in.GetDocumentNumber(), GivenNames: in.GetGivenNames(), Surnames: in.GetSurnames(),
		Email: in.GetEmail(), Phone: in.GetPhone(), Position: in.GetPosition(), IsTeacher: in.GetIsTeacher(), Specialty: in.GetSpecialty(),
		MaxWeeklyHours: intPtr(in.MaxWeeklyHours), HiredOn: hired, Status: in.GetStatus(), Notes: in.GetNotes(),
	}, nil
}

func toBlock(b domain.TimeBlock) *teachingv1.TimeBlock {
	return &teachingv1.TimeBlock{Id: b.ID, Shift: b.Shift, Position: int32(b.Position), StartsAt: b.StartsAt, EndsAt: b.EndsAt, Kind: b.Kind}
}

func toBlocks(items []domain.TimeBlock) []*teachingv1.TimeBlock {
	out := make([]*teachingv1.TimeBlock, len(items))
	for i, b := range items {
		out[i] = toBlock(b)
	}
	return out
}

func toAssignment(a domain.Assignment) *teachingv1.Assignment {
	return &teachingv1.Assignment{Id: a.ID, PeriodId: a.PeriodID, SectionId: a.SectionID, SubjectId: a.SubjectID, SubjectName: a.SubjectName, SubjectKind: a.SubjectKind,
		SubjectCode: a.SubjectCode, StaffId: a.StaffID, StaffName: a.StaffName, WeeklyHours: int32(a.WeeklyHours), Scheduled: int32(a.Scheduled),
		SectionLabel: a.SectionLabel, PeriodName: a.PeriodName}
}

func toAssignments(items []domain.Assignment) []*teachingv1.Assignment {
	out := make([]*teachingv1.Assignment, len(items))
	for i, a := range items {
		out[i] = toAssignment(a)
	}
	return out
}

func toEntry(e domain.ScheduleEntry) *teachingv1.ScheduleEntry {
	return &teachingv1.ScheduleEntry{Id: e.ID, PeriodId: e.PeriodID, SectionId: e.SectionID, SectionLabel: e.SectionLabel, Shift: e.Shift, AssignmentId: e.AssignmentID,
		SubjectName: e.SubjectName, StaffId: e.StaffID, StaffName: e.StaffName, Weekday: int32(e.Weekday), BlockId: e.BlockID, StartsAt: e.StartsAt, EndsAt: e.EndsAt, Room: e.Room}
}

func toEntries(items []domain.ScheduleEntry) []*teachingv1.ScheduleEntry {
	out := make([]*teachingv1.ScheduleEntry, len(items))
	for i, e := range items {
		out[i] = toEntry(e)
	}
	return out
}

// ---------- staff ----------

func (s *staffServer) ListStaff(ctx context.Context, req *teachingv1.ListStaffRequest) (*teachingv1.ListStaffResponse, error) {
	page, err := s.staff.List(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.StaffFilter{Query: req.GetQuery(), Status: req.GetStatus(), TeachersOnly: req.GetTeachersOnly(), Limit: int(req.GetLimit()), Offset: int(req.GetOffset())})
	if err != nil {
		return nil, logUnexpected(s.logger, "ListStaff", err)
	}
	return &teachingv1.ListStaffResponse{Staff: toStaffList(page.Staff), Total: int32(page.Total), CanManage: page.CanManage, CanGrant: page.CanGrant}, nil
}

func (s *staffServer) GetStaff(ctx context.Context, req *teachingv1.GetStaffRequest) (*teachingv1.GetStaffResponse, error) {
	d, err := s.staff.Get(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, logUnexpected(s.logger, "GetStaff", err)
	}
	return &teachingv1.GetStaffResponse{Staff: toStaff(d.Staff), Assignments: toAssignments(d.Assignments), CanManage: d.CanManage, CanGrant: d.CanGrant}, nil
}

func (s *staffServer) CreateStaff(ctx context.Context, req *teachingv1.CreateStaffRequest) (*teachingv1.CreateStaffResponse, error) {
	in, err := staffInput(req.GetStaff())
	if err != nil {
		return nil, logUnexpected(s.logger, "CreateStaff", err)
	}
	st, err := s.staff.Create(ctx, bearer(ctx), req.GetInstitutionSlug(), in)
	if err != nil {
		return nil, logUnexpected(s.logger, "CreateStaff", err)
	}
	return &teachingv1.CreateStaffResponse{Staff: toStaff(st)}, nil
}

func (s *staffServer) UpdateStaff(ctx context.Context, req *teachingv1.UpdateStaffRequest) (*teachingv1.UpdateStaffResponse, error) {
	in, err := staffInput(req.GetStaff())
	if err != nil {
		return nil, logUnexpected(s.logger, "UpdateStaff", err)
	}
	st, err := s.staff.Update(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), in)
	if err != nil {
		return nil, logUnexpected(s.logger, "UpdateStaff", err)
	}
	return &teachingv1.UpdateStaffResponse{Staff: toStaff(st)}, nil
}

func (s *staffServer) DeleteStaff(ctx context.Context, req *teachingv1.DeleteStaffRequest) (*teachingv1.DeleteStaffResponse, error) {
	if err := s.staff.Delete(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, logUnexpected(s.logger, "DeleteStaff", err)
	}
	return &teachingv1.DeleteStaffResponse{}, nil
}

func (s *staffServer) GrantRole(ctx context.Context, req *teachingv1.GrantRoleRequest) (*teachingv1.GrantRoleResponse, error) {
	d, err := s.staff.GrantRole(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetRole())
	if err != nil {
		return nil, logUnexpected(s.logger, "GrantRole", err)
	}
	return &teachingv1.GrantRoleResponse{Staff: toStaff(d.Staff)}, nil
}

// ---------- teaching ----------

func (s *teachingServer) GetPlanner(ctx context.Context, req *teachingv1.GetPlannerRequest) (*teachingv1.GetPlannerResponse, error) {
	p, err := s.teaching.Planner(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, logUnexpected(s.logger, "GetPlanner", err)
	}
	return &teachingv1.GetPlannerResponse{Context: toContext(p.Context), Period: toPeriod(p.Period), Sections: toSections(p.Sections), Blocks: toBlocks(p.Blocks),
		Assignments: toAssignments(p.Assignments), Entries: toEntries(p.Entries), Teachers: toStaffList(p.Teachers), Elsewhere: toEntries(p.Elsewhere)}, nil
}

func (s *teachingServer) PrepareLoad(ctx context.Context, req *teachingv1.PrepareLoadRequest) (*teachingv1.PrepareLoadResponse, error) {
	n, err := s.teaching.PrepareLoad(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, logUnexpected(s.logger, "PrepareLoad", err)
	}
	return &teachingv1.PrepareLoadResponse{Added: int32(n)}, nil
}

func (s *teachingServer) UpdateAssignment(ctx context.Context, req *teachingv1.UpdateAssignmentRequest) (*teachingv1.UpdateAssignmentResponse, error) {
	a, err := s.teaching.UpdateAssignment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetStaffId(), int(req.GetWeeklyHours()))
	if err != nil {
		return nil, logUnexpected(s.logger, "UpdateAssignment", err)
	}
	return &teachingv1.UpdateAssignmentResponse{Assignment: toAssignment(a)}, nil
}

func (s *teachingServer) DeleteAssignment(ctx context.Context, req *teachingv1.DeleteAssignmentRequest) (*teachingv1.DeleteAssignmentResponse, error) {
	if err := s.teaching.DeleteAssignment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, logUnexpected(s.logger, "DeleteAssignment", err)
	}
	return &teachingv1.DeleteAssignmentResponse{}, nil
}

func (s *teachingServer) SetTutor(ctx context.Context, req *teachingv1.SetTutorRequest) (*teachingv1.SetTutorResponse, error) {
	if err := s.teaching.SetTutor(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), req.GetStaffId()); err != nil {
		return nil, logUnexpected(s.logger, "SetTutor", err)
	}
	return &teachingv1.SetTutorResponse{}, nil
}

func (s *teachingServer) SetTimeBlocks(ctx context.Context, req *teachingv1.SetTimeBlocksRequest) (*teachingv1.SetTimeBlocksResponse, error) {
	blocks := make([]domain.TimeBlock, len(req.GetBlocks()))
	for i, b := range req.GetBlocks() {
		blocks[i] = domain.TimeBlock{StartsAt: b.GetStartsAt(), EndsAt: b.GetEndsAt(), Kind: b.GetKind()}
	}
	out, err := s.teaching.SetTimeBlocks(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetShift(), blocks)
	if err != nil {
		return nil, logUnexpected(s.logger, "SetTimeBlocks", err)
	}
	return &teachingv1.SetTimeBlocksResponse{Blocks: toBlocks(out)}, nil
}

func (s *teachingServer) PlaceClass(ctx context.Context, req *teachingv1.PlaceClassRequest) (*teachingv1.PlaceClassResponse, error) {
	e, err := s.teaching.PlaceClass(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Slot{SectionID: req.GetSectionId(), AssignmentID: req.GetAssignmentId(), Weekday: int(req.GetWeekday()), BlockID: req.GetBlockId()}, req.GetRoom())
	if err != nil {
		return nil, logUnexpected(s.logger, "PlaceClass", err)
	}
	return &teachingv1.PlaceClassResponse{Entry: toEntry(e)}, nil
}

func (s *teachingServer) RemoveClass(ctx context.Context, req *teachingv1.RemoveClassRequest) (*teachingv1.RemoveClassResponse, error) {
	if err := s.teaching.RemoveClass(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, logUnexpected(s.logger, "RemoveClass", err)
	}
	return &teachingv1.RemoveClassResponse{}, nil
}

func (s *teachingServer) ClearSection(ctx context.Context, req *teachingv1.ClearSectionRequest) (*teachingv1.ClearSectionResponse, error) {
	if err := s.teaching.ClearSection(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId()); err != nil {
		return nil, logUnexpected(s.logger, "ClearSection", err)
	}
	return &teachingv1.ClearSectionResponse{}, nil
}

func (s *teachingServer) AutoFill(ctx context.Context, req *teachingv1.AutoFillRequest) (*teachingv1.AutoFillResponse, error) {
	r, err := s.teaching.AutoFill(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetSectionId())
	if err != nil {
		return nil, logUnexpected(s.logger, "AutoFill", err)
	}
	return &teachingv1.AutoFillResponse{Placed: int32(r.Placed), Pending: int32(r.Pending)}, nil
}

func (s *teachingServer) MySchedule(ctx context.Context, req *teachingv1.MyScheduleRequest) (*teachingv1.MyScheduleResponse, error) {
	m, err := s.teaching.Mine(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, logUnexpected(s.logger, "MySchedule", err)
	}
	out := &teachingv1.MyScheduleResponse{Kind: m.Kind, WeekDays: toInt32s(m.WeekDays), Blocks: toBlocks(m.Blocks)}
	for _, g := range m.Groups {
		out.Groups = append(out.Groups, &teachingv1.ScheduleGroup{Title: g.Title, Detail: g.Detail, Entries: toEntries(g.Entries)})
	}
	return out, nil
}
