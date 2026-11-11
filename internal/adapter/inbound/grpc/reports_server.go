package grpc

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/reportsv1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// reportsServer exposes service.ReportsService to joseph-app's server
type reportsServer struct {
	reportsv1.UnimplementedReportsServiceServer
	reports *service.ReportsService
	logger  zerolog.Logger
}

func (s *reportsServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("reports grpc call failed")
	}
	return fromDomain(err)
}

func toStatusCount(c domain.StatusCount) *reportsv1.StatusCount {
	return &reportsv1.StatusCount{Present: int32(c.Present), Late: int32(c.Late), Absent: int32(c.Absent), Justified: int32(c.Justified)}
}

func toGroupAttendance(list []domain.GroupAttendance) []*reportsv1.GroupAttendance {
	out := make([]*reportsv1.GroupAttendance, 0, len(list))
	for _, g := range list {
		out = append(out, &reportsv1.GroupAttendance{Label: g.Label, Counts: toStatusCount(g.StatusCount)})
	}
	return out
}

func toResultMix(list []domain.ResultMix) []*reportsv1.ResultMix {
	out := make([]*reportsv1.ResultMix, 0, len(list))
	for _, m := range list {
		out = append(out, &reportsv1.ResultMix{Label: m.Label, Ad: int32(m.AD), A: int32(m.A), B: int32(m.B), C: int32(m.C), Average: m.Average, Max: m.Max,
			Approved: int32(m.Approved), Failed: int32(m.Failed), Count: int32(m.Count)})
	}
	return out
}

func (s *reportsServer) GetBoard(ctx context.Context, req *reportsv1.GetBoardRequest) (*reportsv1.GetBoardResponse, error) {
	from, err := optionalDate("from", req.GetFrom())
	if err != nil {
		return nil, fromDomain(err)
	}
	to, err := optionalDate("to", req.GetTo())
	if err != nil {
		return nil, fromDomain(err)
	}
	b, err := s.reports.Board(ctx, bearer(ctx), req.GetInstitutionSlug(), service.ReportQuery{PeriodID: req.GetPeriodId(), LevelID: req.GetLevelId(), Subject: req.GetSubject(), From: from, To: to})
	if err != nil {
		return nil, s.fail("GetBoard", err)
	}
	e := b.Enrollment
	out := &reportsv1.GetBoardResponse{Periods: toPeriods(b.Periods), LevelId: b.LevelID, Subject: b.Subject, Scale: b.Scale, Passing: int32(b.Passing),
		Today: formatDate(b.Today), Academic: b.Academic,
		Enrollment: &reportsv1.Enrollment{Active: int32(e.Active), Withdrawn: int32(e.Withdrawn), Transferred: int32(e.Transferred), Completed: int32(e.Completed),
			New: int32(e.New), Continuing: int32(e.Continuing), TransferIn: int32(e.TransferIn), ReEntry: int32(e.ReEntry), Capacity: int32(e.Capacity)},
		AttendanceGroups: toGroupAttendance(b.AttendanceGroups), AttendanceTimeline: toGroupAttendance(b.AttendanceTimeline),
		ResultTerms: toResultMix(b.ResultTerms), ResultSubjects: toResultMix(b.ResultSubjects), ResultGroups: toResultMix(b.ResultGroups), ConductTerms: toResultMix(b.ConductTerms),
		Finance: &reportsv1.FinanceTotals{Expected: int64(b.Finance.Expected), Discounts: int64(b.Finance.Discounts), Collected: int64(b.Finance.Collected),
			Overdue: int64(b.Finance.Overdue), Debtors: int32(b.Finance.Debtors), Students: int32(b.Finance.Students)}}
	if b.Period.ID != "" {
		out.Period = toPeriod(b.Period)
		out.From, out.To = formatDate(b.From), formatDate(b.To)
	}
	for _, l := range b.Levels {
		out.Levels = append(out.Levels, &reportsv1.Option{Id: l.ID, Name: l.Name})
	}
	for _, g := range e.Groups {
		out.Enrollment.Groups = append(out.Enrollment.Groups, &reportsv1.GroupEnrollment{Label: g.Label, Active: int32(g.Active), Capacity: int32(g.Capacity)})
	}
	for _, d := range b.AttendanceDays {
		out.AttendanceDays = append(out.AttendanceDays, &reportsv1.AttendanceDay{Day: formatDate(d.Day), Counts: toStatusCount(d.StatusCount)})
	}
	for _, m := range b.Months {
		out.Months = append(out.Months, &reportsv1.MoneyFlow{Month: m.Month, Expected: int64(m.Expected), Collected: int64(m.Collected), Received: int64(m.Received)})
	}
	for _, m := range b.Methods {
		out.Methods = append(out.Methods, &reportsv1.MethodTotal{Method: m.Method, Amount: int64(m.Amount), Count: int32(m.Count)})
	}
	for _, a := range b.Aging {
		out.Aging = append(out.Aging, &reportsv1.AgingBucket{Label: a.Label, Amount: int64(a.Amount), Students: int32(a.Students)})
	}
	for _, g := range b.FinanceByGrp {
		out.FinanceGroups = append(out.FinanceGroups, &reportsv1.GroupDebt{Label: g.Label, Expected: int64(g.Expected), Collected: int64(g.Collected), Overdue: int64(g.Overdue), Debtors: int32(g.Debtors)})
	}
	for _, k := range b.Compare {
		out.Compare = append(out.Compare, &reportsv1.PeriodKpi{PeriodId: k.PeriodID, Name: k.Name, Status: k.Status, Students: int32(k.Students),
			Attendance: k.Attendance, Achievement: k.Achievement, Collection: k.Collection})
	}
	return out, nil
}
