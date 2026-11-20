package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/attendancev1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// attendanceServer exposes service.AttendanceService to lt-edu-app's server
type attendanceServer struct {
	attendancev1.UnimplementedAttendanceServiceServer
	attendance *service.AttendanceService
	logger     zerolog.Logger
}

func (s *attendanceServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("attendance grpc call failed")
	}
	return fromDomain(err)
}

func instant(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func toTally(t domain.Tally) *attendancev1.Tally {
	return &attendancev1.Tally{Present: int32(t.Present), Late: int32(t.Late), Absent: int32(t.Absent), Justified: int32(t.Justified)}
}

func toHolder(h domain.CardHolder) *attendancev1.CardHolder {
	return &attendancev1.CardHolder{Kind: h.Kind, Id: h.ID, Name: h.Name, Code: h.Code, Detail: h.Detail}
}

func toSectionDay(d domain.SectionDay) *attendancev1.SectionDay {
	return &attendancev1.SectionDay{SectionId: d.SectionID, SectionLabel: d.SectionLabel, LevelName: d.LevelName, Shift: d.Shift, TutorName: d.TutorName,
		Enrolled: int32(d.Enrolled), Tally: toTally(domain.Tally{Present: d.Present, Late: d.Late, Absent: d.Absent, Justified: d.Justified})}
}

func toMarks(in []*attendancev1.MarkInput) []service.MarkInput {
	out := make([]service.MarkInput, len(in))
	for i, m := range in {
		out[i] = service.MarkInput{PersonID: m.GetPersonId(), Status: m.GetStatus(), Note: m.GetNote()}
	}
	return out
}

func toSlot(c domain.ClassSlot) *attendancev1.ClassSlot {
	return &attendancev1.ClassSlot{AssignmentId: c.AssignmentID, SectionId: c.SectionID, SectionLabel: c.SectionLabel, SubjectName: c.SubjectName, StaffName: c.StaffName,
		BlockId: c.BlockID, StartsAt: c.StartsAt, EndsAt: c.EndsAt, Room: c.Room, SessionId: c.SessionID, Topic: c.Topic, Absent: int32(c.Absent), Students: int32(c.Students)}
}

func toJustification(j domain.Justification) *attendancev1.Justification {
	return &attendancev1.Justification{Id: j.ID, StudentId: j.StudentID, StudentName: j.StudentName, SectionLabel: j.SectionLabel, FromDay: formatDate(j.FromDay),
		ToDay: formatDate(j.ToDay), Reason: j.Reason, Description: j.Description, Status: j.Status, SubmittedName: j.SubmittedName,
		SubmittedAt: instant(&j.SubmittedAt), ReviewedName: j.ReviewedName, ReviewedAt: instant(j.ReviewedAt), ReviewNote: j.ReviewNote, AbsentDays: int32(j.AbsentDays)}
}

func toJustifications(items []domain.Justification) []*attendancev1.Justification {
	out := make([]*attendancev1.Justification, len(items))
	for i, j := range items {
		out[i] = toJustification(j)
	}
	return out
}

func toSubjectTallies(items []domain.SubjectTally) []*attendancev1.SubjectTally {
	out := make([]*attendancev1.SubjectTally, len(items))
	for i, t := range items {
		out[i] = &attendancev1.SubjectTally{StudentId: t.StudentID, StudentName: t.StudentName, SectionLabel: t.SectionLabel, AssignmentId: t.AssignmentID,
			SubjectName: t.SubjectName, Classes: toTally(t.Classes)}
	}
	return out
}

func toDailyMark(m domain.DailyMark) *attendancev1.DailyMark {
	return &attendancev1.DailyMark{Day: formatDate(m.Day), Status: m.Status, ArrivedAt: instant(m.ArrivedAt), Source: m.Source, Note: m.Note}
}

func toStudentAttendance(a service.StudentAttendance) *attendancev1.StudentAttendance {
	out := &attendancev1.StudentAttendance{Student: toHolder(a.Student), SectionLabel: a.SectionLabel, Limit: int32(a.Limit), Days: toTally(a.Days),
		Subjects: toSubjectTallies(a.Subjects), Justifications: toJustifications(a.Justification)}
	if a.Today != nil {
		out.Today = toDailyMark(*a.Today)
	}
	for _, m := range a.Marks {
		out.Marks = append(out.Marks, toDailyMark(m))
	}
	return out
}

func toRoster(r service.SectionRoster) *attendancev1.GetRosterResponse {
	out := &attendancev1.GetRosterResponse{Day: formatDate(r.Day), SchoolDay: r.SchoolDay, Section: toSectionDay(r.Section), CanEdit: r.CanEdit, LateAfter: r.LateAfter}
	for _, x := range r.Rows {
		out.Rows = append(out.Rows, &attendancev1.RosterRow{StudentId: x.StudentID, Code: x.Code, Name: x.Name, Status: x.Status, ArrivedAt: instant(x.ArrivedAt),
			Source: x.Source, Note: x.Note, PendingJustification: x.PendingJustification})
	}
	return out
}

func toStaffDay(d service.StaffDay) *attendancev1.GetStaffDayResponse {
	out := &attendancev1.GetStaffDayResponse{Day: formatDate(d.Day), SchoolDay: d.SchoolDay, LateAfter: d.LateAfter}
	for _, x := range d.Rows {
		out.Rows = append(out.Rows, &attendancev1.StaffRow{StaffId: x.StaffID, Code: x.Code, Name: x.Name, Position: x.Position, Status: x.Status,
			ArrivedAt: instant(x.ArrivedAt), Source: x.Source, Note: x.Note})
	}
	return out
}

func toSheet(c service.ClassSheet) *attendancev1.GetClassResponse {
	out := &attendancev1.GetClassResponse{Day: formatDate(c.Day), Slot: toSlot(c.Slot), CanEdit: c.CanEdit}
	for _, m := range c.Marks {
		out.Marks = append(out.Marks, &attendancev1.ClassMark{StudentId: m.StudentID, Code: m.Code, Name: m.Name, Status: m.Status, Note: m.Note, DayStatus: m.DayStatus})
	}
	return out
}

func toCardCode(c service.CardCode) *attendancev1.CardCode {
	return &attendancev1.CardCode{Id: c.ID, Payload: c.Payload, Size: int32(c.Size), Modules: c.Modules}
}

func (s *attendanceServer) GetOverview(ctx context.Context, req *attendancev1.GetOverviewRequest) (*attendancev1.GetOverviewResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	o, err := s.attendance.Overview(ctx, bearer(ctx), req.GetInstitutionSlug(), day)
	if err != nil {
		return nil, s.fail("GetOverview", err)
	}
	out := &attendancev1.GetOverviewResponse{Day: formatDate(o.Day), Today: formatDate(o.Today), SchoolDay: o.SchoolDay, LateAfterMinutes: int32(o.Settings.LateAfterMinutes),
		AbsenceLimitPercent: int32(o.Settings.AbsenceLimitPercent), CanScan: o.CanScan, CanManageStaff: o.CanManageStaff, CanReview: o.CanReview,
		CanTakeClasses: o.CanTakeClasses, Tutoring: o.Tutoring, PendingJustifications: int32(o.PendingJustifications),
		Vocabulary: &attendancev1.Vocabulary{Level: o.Vocabulary.Level, Grade: o.Vocabulary.Grade, Section: o.Vocabulary.Section, Period: o.Vocabulary.Period},
		StaffTally: toTally(o.StaffTally), StaffTotal: int32(o.StaffTotal), WeekDays: toInt32s(o.Settings.WeekDays)}
	for _, d := range o.Sections {
		out.Sections = append(out.Sections, toSectionDay(d))
	}
	return out, nil
}

func (s *attendanceServer) Scan(ctx context.Context, req *attendancev1.ScanRequest) (*attendancev1.ScanResponse, error) {
	r, err := s.attendance.Scan(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetInput())
	if err != nil {
		return nil, s.fail("Scan", err)
	}
	return &attendancev1.ScanResponse{Holder: toHolder(r.Holder), Status: r.Status, ArrivedAt: instant(&r.ArrivedAt), Repeated: r.Repeated}, nil
}

func (s *attendanceServer) RecentScans(ctx context.Context, req *attendancev1.RecentScansRequest) (*attendancev1.RecentScansResponse, error) {
	scans, err := s.attendance.RecentScans(ctx, bearer(ctx), req.GetInstitutionSlug(), int(req.GetLimit()))
	if err != nil {
		return nil, s.fail("RecentScans", err)
	}
	out := &attendancev1.RecentScansResponse{}
	for _, g := range scans {
		out.Scans = append(out.Scans, &attendancev1.GateScan{Id: g.ID, Holder: toHolder(g.Holder), ScannedAt: instant(&g.ScannedAt), Source: g.Source, Status: g.Status})
	}
	return out, nil
}

func (s *attendanceServer) GetRoster(ctx context.Context, req *attendancev1.GetRosterRequest) (*attendancev1.GetRosterResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	r, err := s.attendance.Roster(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), day)
	if err != nil {
		return nil, s.fail("GetRoster", err)
	}
	return toRoster(r), nil
}

func (s *attendanceServer) MarkStudents(ctx context.Context, req *attendancev1.MarkStudentsRequest) (*attendancev1.MarkStudentsResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	r, err := s.attendance.MarkStudents(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), day, toMarks(req.GetMarks()))
	if err != nil {
		return nil, s.fail("MarkStudents", err)
	}
	return &attendancev1.MarkStudentsResponse{Roster: toRoster(r)}, nil
}

func (s *attendanceServer) CloseDay(ctx context.Context, req *attendancev1.CloseDayRequest) (*attendancev1.CloseDayResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	n, err := s.attendance.CloseDay(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), day)
	if err != nil {
		return nil, s.fail("CloseDay", err)
	}
	return &attendancev1.CloseDayResponse{Marked: int32(n)}, nil
}

func (s *attendanceServer) GetStaffDay(ctx context.Context, req *attendancev1.GetStaffDayRequest) (*attendancev1.GetStaffDayResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	d, err := s.attendance.StaffDay(ctx, bearer(ctx), req.GetInstitutionSlug(), day)
	if err != nil {
		return nil, s.fail("GetStaffDay", err)
	}
	return toStaffDay(d), nil
}

func (s *attendanceServer) MarkStaff(ctx context.Context, req *attendancev1.MarkStaffRequest) (*attendancev1.MarkStaffResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	d, err := s.attendance.MarkStaff(ctx, bearer(ctx), req.GetInstitutionSlug(), day, toMarks(req.GetMarks()))
	if err != nil {
		return nil, s.fail("MarkStaff", err)
	}
	return &attendancev1.MarkStaffResponse{Day: toStaffDay(d)}, nil
}

func (s *attendanceServer) CloseStaffDay(ctx context.Context, req *attendancev1.CloseStaffDayRequest) (*attendancev1.CloseStaffDayResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	n, err := s.attendance.CloseStaffDay(ctx, bearer(ctx), req.GetInstitutionSlug(), day)
	if err != nil {
		return nil, s.fail("CloseStaffDay", err)
	}
	return &attendancev1.CloseStaffDayResponse{Marked: int32(n)}, nil
}

func (s *attendanceServer) ListClasses(ctx context.Context, req *attendancev1.ListClassesRequest) (*attendancev1.ListClassesResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	c, err := s.attendance.Classes(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetSectionId(), day)
	if err != nil {
		return nil, s.fail("ListClasses", err)
	}
	out := &attendancev1.ListClassesResponse{Day: formatDate(c.Day), SchoolDay: c.SchoolDay, Scope: c.Scope}
	for _, x := range c.Slots {
		out.Slots = append(out.Slots, toSlot(x))
	}
	return out, nil
}

func (s *attendanceServer) GetClass(ctx context.Context, req *attendancev1.GetClassRequest) (*attendancev1.GetClassResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	c, err := s.attendance.Class(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetBlockId(), day)
	if err != nil {
		return nil, s.fail("GetClass", err)
	}
	return toSheet(c), nil
}

func (s *attendanceServer) SaveClass(ctx context.Context, req *attendancev1.SaveClassRequest) (*attendancev1.SaveClassResponse, error) {
	day, err := optionalDate("day", req.GetDay())
	if err != nil {
		return nil, err
	}
	c, err := s.attendance.SaveClass(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetAssignmentId(), req.GetBlockId(), day, req.GetTopic(), toMarks(req.GetMarks()))
	if err != nil {
		return nil, s.fail("SaveClass", err)
	}
	return &attendancev1.SaveClassResponse{Sheet: toSheet(c)}, nil
}

func (s *attendanceServer) ListJustifications(ctx context.Context, req *attendancev1.ListJustificationsRequest) (*attendancev1.ListJustificationsResponse, error) {
	p, err := s.attendance.Justifications(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStatus(), req.GetStudentId(), int(req.GetLimit()), int(req.GetOffset()))
	if err != nil {
		return nil, s.fail("ListJustifications", err)
	}
	return &attendancev1.ListJustificationsResponse{Items: toJustifications(p.Items), Total: int32(p.Total), CanReview: p.CanReview}, nil
}

func (s *attendanceServer) SubmitJustification(ctx context.Context, req *attendancev1.SubmitJustificationRequest) (*attendancev1.SubmitJustificationResponse, error) {
	from, err := optionalDate("fromDay", req.GetFromDay())
	if err != nil {
		return nil, err
	}
	to, err := optionalDate("toDay", req.GetToDay())
	if err != nil {
		return nil, err
	}
	in := service.JustificationInput{StudentID: req.GetStudentId(), Reason: req.GetReason(), Description: req.GetDescription()}
	if from != nil {
		in.FromDay = *from
	}
	if to != nil {
		in.ToDay = *to
	}
	j, err := s.attendance.SubmitJustification(ctx, bearer(ctx), req.GetInstitutionSlug(), in)
	if err != nil {
		return nil, s.fail("SubmitJustification", err)
	}
	return &attendancev1.SubmitJustificationResponse{Justification: toJustification(j)}, nil
}

func (s *attendanceServer) ReviewJustification(ctx context.Context, req *attendancev1.ReviewJustificationRequest) (*attendancev1.ReviewJustificationResponse, error) {
	j, err := s.attendance.ReviewJustification(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetApprove(), req.GetNote())
	if err != nil {
		return nil, s.fail("ReviewJustification", err)
	}
	return &attendancev1.ReviewJustificationResponse{Justification: toJustification(j)}, nil
}

func (s *attendanceServer) GetReport(ctx context.Context, req *attendancev1.GetReportRequest) (*attendancev1.GetReportResponse, error) {
	r, err := s.attendance.Report(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetSectionId())
	if err != nil {
		return nil, s.fail("GetReport", err)
	}
	out := &attendancev1.GetReportResponse{PeriodId: r.PeriodID, Limit: int32(r.Limit), Subjects: toSubjectTallies(r.Subjects)}
	for _, t := range r.Students {
		out.Students = append(out.Students, &attendancev1.StudentTally{StudentId: t.StudentID, Code: t.Code, Name: t.Name, SectionId: t.SectionID, SectionLabel: t.SectionLabel, Days: toTally(t.Days)})
	}
	return out, nil
}

func (s *attendanceServer) GetStudent(ctx context.Context, req *attendancev1.GetStudentRequest) (*attendancev1.GetStudentResponse, error) {
	a, err := s.attendance.Student(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("GetStudent", err)
	}
	return &attendancev1.GetStudentResponse{Attendance: toStudentAttendance(a)}, nil
}

func (s *attendanceServer) MyAttendance(ctx context.Context, req *attendancev1.MyAttendanceRequest) (*attendancev1.MyAttendanceResponse, error) {
	items, err := s.attendance.Mine(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("MyAttendance", err)
	}
	out := &attendancev1.MyAttendanceResponse{}
	for _, a := range items {
		out.Students = append(out.Students, toStudentAttendance(a))
	}
	return out, nil
}

func (s *attendanceServer) CardCodes(ctx context.Context, req *attendancev1.CardCodesRequest) (*attendancev1.CardCodesResponse, error) {
	codes, err := s.attendance.CardCodes(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetKind(), req.GetIds())
	if err != nil {
		return nil, s.fail("CardCodes", err)
	}
	out := &attendancev1.CardCodesResponse{}
	for _, c := range codes {
		out.Codes = append(out.Codes, toCardCode(c))
	}
	return out, nil
}

func (s *attendanceServer) RenewCard(ctx context.Context, req *attendancev1.RenewCardRequest) (*attendancev1.RenewCardResponse, error) {
	c, err := s.attendance.RenewCard(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetKind(), req.GetId())
	if err != nil {
		return nil, s.fail("RenewCard", err)
	}
	return &attendancev1.RenewCardResponse{Code: toCardCode(c)}, nil
}
