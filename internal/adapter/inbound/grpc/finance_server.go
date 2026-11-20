package grpc

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"api/internal/adapter/inbound/grpc/gen/financev1"
	"api/internal/core/domain"
	"api/internal/core/service"
)

// financeServer exposes service.FinanceService to lt-edu-app's server
type financeServer struct {
	financev1.UnimplementedFinanceServiceServer
	finance *service.FinanceService
	logger  zerolog.Logger
}

func (s *financeServer) fail(method string, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		s.logger.Error().Err(err).Str("method", method).Msg("finance grpc call failed")
	}
	return fromDomain(err)
}

func toPlan(p domain.FeePlan) *financev1.FeePlan {
	out := &financev1.FeePlan{Id: p.ID, PeriodId: p.PeriodID, PeriodName: p.PeriodName, LevelId: p.LevelID, LevelName: p.LevelName, Name: p.Name, Students: int32(p.Students)}
	for _, i := range p.Items {
		out.Items = append(out.Items, &financev1.FeeItem{Id: i.ID, Concept: i.Concept, Name: i.Name, Amount: int64(i.Amount), DueOn: formatDate(i.DueOn)})
	}
	return out
}

func toPaymentMsg(p domain.Payment) *financev1.Payment {
	out := &financev1.Payment{Id: p.ID, StudentId: p.StudentID, StudentName: p.StudentName, StudentCode: p.StudentCode, Receipt: p.Receipt, PaidOn: formatDate(p.PaidOn),
		Amount: int64(p.Amount), Method: p.Method, Reference: p.Reference, Payer: p.Payer, Notes: p.Notes, Status: p.Status, AnnulReason: p.AnnulReason, ReceivedName: p.ReceivedName}
	for _, a := range p.Allocations {
		out.Allocations = append(out.Allocations, &financev1.Allocation{ChargeId: a.ChargeID, ChargeName: a.ChargeName, Amount: int64(a.Amount)})
	}
	return out
}

func toStatement(st service.Statement) *financev1.Statement {
	out := &financev1.Statement{StudentId: st.Student.ID, StudentName: st.Student.Name, StudentCode: st.Student.Code, Balance: int64(st.Balance), Overdue: int64(st.Overdue),
		CanManage: st.CanManage, Today: formatDate(st.Today)}
	for _, c := range st.Charges {
		out.Charges = append(out.Charges, &financev1.Charge{Id: c.ID, PeriodId: c.PeriodID, PeriodName: c.PeriodName, Concept: c.Concept, Name: c.Name, Amount: int64(c.Amount),
			Discount: int64(c.Discount), Paid: int64(c.Paid), Balance: int64(c.Balance()), DueOn: formatDate(c.DueOn), Status: c.Status, VoidReason: c.VoidReason})
	}
	for _, p := range st.Payments {
		out.Payments = append(out.Payments, toPaymentMsg(p))
	}
	for _, d := range st.Discounts {
		out.Discounts = append(out.Discounts, &financev1.Discount{StudentId: d.StudentID, StudentName: d.StudentName, PeriodId: d.PeriodID, Percent: d.Percent, TuitionOnly: d.TuitionOnly, Reason: d.Reason})
	}
	for _, r := range st.Reminders {
		out.Reminders = append(out.Reminders, &financev1.Reminder{Id: r.ID, Message: r.Message, Overdue: int64(r.Overdue), SentName: r.SentName, SentAt: instant(&r.SentAt)})
	}
	return out
}

func dateOr(field, v string) (time.Time, error) {
	d, err := optionalDate(field, v)
	if err != nil || d == nil {
		return time.Time{}, err
	}
	return *d, nil
}

func (s *financeServer) GetSummary(ctx context.Context, req *financev1.GetSummaryRequest) (*financev1.GetSummaryResponse, error) {
	o, err := s.finance.Summary(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("GetSummary", err)
	}
	out := &financev1.GetSummaryResponse{Periods: toPeriods(o.Periods), Expected: int64(o.Expected), Discounts: int64(o.Discounts), Collected: int64(o.Collected), Pending: int64(o.Pending),
		Overdue: int64(o.Overdue), Debtors: int32(o.Debtors), Students: int32(o.Students), CanManage: o.CanManage, Today: formatDate(o.Today)}
	if o.Period.ID != "" {
		out.Period = toPeriod(o.Period)
	}
	for _, m := range o.Months {
		out.Months = append(out.Months, &financev1.MonthTotal{Month: m.Month, Expected: int64(m.Expected), Collected: int64(m.Collected)})
	}
	for _, a := range o.Accounts {
		out.Accounts = append(out.Accounts, &financev1.Account{StudentId: a.StudentID, Code: a.Code, Name: a.Name, Placement: a.Placement, LevelId: a.LevelID, Charged: int64(a.Charged),
			Discounts: int64(a.Discounts), Paid: int64(a.Paid), Overdue: int64(a.Overdue), OverdueCount: int32(a.OverdueCount), OldestDue: datePtr(a.OldestDue), LastReminder: instant(a.LastReminder)})
	}
	return out, nil
}

func (s *financeServer) ListPlans(ctx context.Context, req *financev1.ListPlansRequest) (*financev1.ListPlansResponse, error) {
	plans, can, err := s.finance.Plans(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("ListPlans", err)
	}
	out := &financev1.ListPlansResponse{CanManage: can}
	for _, p := range plans {
		out.Plans = append(out.Plans, toPlan(p))
	}
	return out, nil
}

func (s *financeServer) SavePlan(ctx context.Context, req *financev1.SavePlanRequest) (*financev1.SavePlanResponse, error) {
	in := req.GetPlan()
	p := domain.FeePlan{ID: in.GetId(), PeriodID: in.GetPeriodId(), LevelID: in.GetLevelId(), Name: in.GetName()}
	for i, it := range in.GetItems() {
		due, err := dateOr("items", it.GetDueOn())
		if err != nil {
			return nil, err
		}
		p.Items = append(p.Items, domain.FeeItem{ID: it.GetId(), Concept: it.GetConcept(), Name: it.GetName(), Amount: domain.Money(it.GetAmount()), DueOn: due, Position: i})
	}
	out, err := s.finance.SavePlan(ctx, bearer(ctx), req.GetInstitutionSlug(), p)
	if err != nil {
		return nil, s.fail("SavePlan", err)
	}
	return &financev1.SavePlanResponse{Plan: toPlan(out)}, nil
}

func (s *financeServer) DeletePlan(ctx context.Context, req *financev1.DeletePlanRequest) (*financev1.DeletePlanResponse, error) {
	if err := s.finance.DeletePlan(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId()); err != nil {
		return nil, s.fail("DeletePlan", err)
	}
	return &financev1.DeletePlanResponse{}, nil
}

func (s *financeServer) GenerateCharges(ctx context.Context, req *financev1.GenerateChargesRequest) (*financev1.GenerateChargesResponse, error) {
	n, err := s.finance.Generate(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GenerateCharges", err)
	}
	return &financev1.GenerateChargesResponse{Created: int32(n)}, nil
}

func (s *financeServer) GetStatement(ctx context.Context, req *financev1.GetStatementRequest) (*financev1.GetStatementResponse, error) {
	st, err := s.finance.Statement(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId())
	if err != nil {
		return nil, s.fail("GetStatement", err)
	}
	return &financev1.GetStatementResponse{Statement: toStatement(st)}, nil
}

func (s *financeServer) MyStatements(ctx context.Context, req *financev1.MyStatementsRequest) (*financev1.MyStatementsResponse, error) {
	list, err := s.finance.Mine(ctx, bearer(ctx), req.GetInstitutionSlug())
	if err != nil {
		return nil, s.fail("MyStatements", err)
	}
	out := &financev1.MyStatementsResponse{}
	for _, st := range list {
		out.Statements = append(out.Statements, toStatement(st))
	}
	return out, nil
}

func (s *financeServer) RegisterPayment(ctx context.Context, req *financev1.RegisterPaymentRequest) (*financev1.RegisterPaymentResponse, error) {
	paid, err := dateOr("paidOn", req.GetPaidOn())
	if err != nil {
		return nil, err
	}
	p, err := s.finance.RegisterPayment(ctx, bearer(ctx), req.GetInstitutionSlug(), service.PaymentInput{StudentID: req.GetStudentId(), PaidOn: paid, Amount: domain.Money(req.GetAmount()),
		Method: req.GetMethod(), Reference: req.GetReference(), Payer: req.GetPayer(), Notes: req.GetNotes(), ChargeIDs: req.GetChargeIds()})
	if err != nil {
		return nil, s.fail("RegisterPayment", err)
	}
	return &financev1.RegisterPaymentResponse{Payment: toPaymentMsg(p)}, nil
}

func (s *financeServer) AnnulPayment(ctx context.Context, req *financev1.AnnulPaymentRequest) (*financev1.AnnulPaymentResponse, error) {
	if err := s.finance.AnnulPayment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetReason()); err != nil {
		return nil, s.fail("AnnulPayment", err)
	}
	return &financev1.AnnulPaymentResponse{}, nil
}

func (s *financeServer) GetPayment(ctx context.Context, req *financev1.GetPaymentRequest) (*financev1.GetPaymentResponse, error) {
	p, err := s.finance.Payment(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId())
	if err != nil {
		return nil, s.fail("GetPayment", err)
	}
	return &financev1.GetPaymentResponse{Payment: toPaymentMsg(p), Institution: s.finance.InstitutionName(ctx, bearer(ctx), req.GetInstitutionSlug())}, nil
}

func (s *financeServer) GetCash(ctx context.Context, req *financev1.GetCashRequest) (*financev1.GetCashResponse, error) {
	from, err := dateOr("from", req.GetFrom())
	if err != nil {
		return nil, err
	}
	to, err := dateOr("to", req.GetTo())
	if err != nil {
		return nil, err
	}
	c, err := s.finance.Cash(ctx, bearer(ctx), req.GetInstitutionSlug(), from, to, req.GetMethod())
	if err != nil {
		return nil, s.fail("GetCash", err)
	}
	out := &financev1.GetCashResponse{From: formatDate(c.From), To: formatDate(c.To), ByMethod: map[string]int64{}, Total: int64(c.Total), Annulled: int32(c.Annulled)}
	for k, v := range c.ByMethod {
		out.ByMethod[k] = int64(v)
	}
	for _, p := range c.Payments {
		out.Payments = append(out.Payments, toPaymentMsg(p))
	}
	return out, nil
}

func (s *financeServer) SaveDiscount(ctx context.Context, req *financev1.SaveDiscountRequest) (*financev1.SaveDiscountResponse, error) {
	d := req.GetDiscount()
	st, err := s.finance.SaveDiscount(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Discount{StudentID: d.GetStudentId(), PeriodID: d.GetPeriodId(), Percent: d.GetPercent(),
		TuitionOnly: d.GetTuitionOnly(), Reason: d.GetReason()})
	if err != nil {
		return nil, s.fail("SaveDiscount", err)
	}
	return &financev1.SaveDiscountResponse{Statement: toStatement(st)}, nil
}

func (s *financeServer) DeleteDiscount(ctx context.Context, req *financev1.DeleteDiscountRequest) (*financev1.DeleteDiscountResponse, error) {
	st, err := s.finance.DeleteDiscount(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetStudentId(), req.GetPeriodId())
	if err != nil {
		return nil, s.fail("DeleteDiscount", err)
	}
	return &financev1.DeleteDiscountResponse{Statement: toStatement(st)}, nil
}

func (s *financeServer) VoidCharge(ctx context.Context, req *financev1.VoidChargeRequest) (*financev1.VoidChargeResponse, error) {
	st, err := s.finance.VoidCharge(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetId(), req.GetReason())
	if err != nil {
		return nil, s.fail("VoidCharge", err)
	}
	return &financev1.VoidChargeResponse{Statement: toStatement(st)}, nil
}

func (s *financeServer) AddCharge(ctx context.Context, req *financev1.AddChargeRequest) (*financev1.AddChargeResponse, error) {
	due, err := dateOr("dueOn", req.GetDueOn())
	if err != nil {
		return nil, err
	}
	st, err := s.finance.AddCharge(ctx, bearer(ctx), req.GetInstitutionSlug(), domain.Charge{StudentID: req.GetStudentId(), PeriodID: req.GetPeriodId(), Name: req.GetName(), Amount: domain.Money(req.GetAmount()), DueOn: due})
	if err != nil {
		return nil, s.fail("AddCharge", err)
	}
	return &financev1.AddChargeResponse{Statement: toStatement(st)}, nil
}

func (s *financeServer) SendReminders(ctx context.Context, req *financev1.SendRemindersRequest) (*financev1.SendRemindersResponse, error) {
	n, err := s.finance.Remind(ctx, bearer(ctx), req.GetInstitutionSlug(), req.GetPeriodId(), req.GetStudentIds())
	if err != nil {
		return nil, s.fail("SendReminders", err)
	}
	return &financev1.SendRemindersResponse{Sent: int32(n)}, nil
}
