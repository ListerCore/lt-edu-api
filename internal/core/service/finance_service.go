package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// EventFinanceChanged: {institution, area (plans | payments | discounts | reminders), student}
const EventFinanceChanged = "finance.changed"

var (
	// Tesorería and dirección charge and collect; secretaría and coordinación consult
	financeManagers = map[string]bool{domain.RoleSuperadmin: true, "director": true, "treasurer": true}
	financeReaders  = map[string]bool{"secretary": true, "academic_coordinator": true}
	paymentMethods  = []string{"cash", "transfer", "yape", "plin", "card", "deposit"}
	// apafa: the parents' association fee (cuota APAFA) of public institutions
	feeConcepts = []string{"enrollment", "tuition", "apafa", "other"}
)

// conceptLabels are the Spanish names of the fee concepts, for messages and documents
var conceptLabels = map[string]string{"enrollment": "Matrícula", "tuition": "Pensión", "apafa": "Cuota APAFA", "other": "Otro concepto"}

// ConceptLabel names a fee concept in Spanish ("" for an unknown one)
func ConceptLabel(concept string) string { return conceptLabels[concept] }

// noTuitionMessage is what a public institution is told when it tries to charge tuition
const noTuitionMessage = "En una institución pública no se cobran pensiones: usa matrícula, cuota APAFA u otro concepto."

// FinanceService runs fees and payments (phase 10 of docs/plan-academico.md): the fee plan of each period and level,
// each student's schedule with their discount, payments with an internal receipt, the accounts and the overdue
// ones, the cash of each day and reminders for families. No late fees and nothing academic is blocked for a debt
// (Ley N.° 29947)
type FinanceService struct {
	repo       port.FinanceRepository
	assessment *AssessmentService
}

func NewFinanceService(repo port.FinanceRepository, assessment *AssessmentService) *FinanceService {
	return &FinanceService{repo: repo, assessment: assessment}
}

type financeScope struct {
	attendanceScope
}

func (sc financeScope) manager() bool { return financeManagers[sc.role] }

func (s *FinanceService) scope(ctx context.Context, token, slug string) (financeScope, error) {
	isc, err := resolveInstitution(ctx, s.assessment.academic.auth, s.assessment.academic.repo.InstitutionBySlug, token, slug)
	if err != nil {
		return financeScope{}, err
	}
	me, err := s.assessment.academic.auth.Principal(ctx, token)
	if err != nil {
		return financeScope{}, err
	}
	sc := financeScope{attendanceScope{academicScope: academicScope{institutionScope: isc, profile: profileFor(isc.institution.Kind)}, email: me.User.Email, userName: me.User.FullName}}
	if !financeManagers[sc.role] && !financeReaders[sc.role] && !sc.family() {
		return sc, domain.ErrFinanceForbidden
	}
	return sc, nil
}

func (s *FinanceService) manage(ctx context.Context, token, slug string) (financeScope, error) {
	sc, err := s.scope(ctx, token, slug)
	if err == nil && !sc.manager() {
		return sc, domain.ErrFinanceReadOnly
	}
	return sc, err
}

func (s *FinanceService) changed(ctx context.Context, sc financeScope, area, studentID string) {
	s.assessment.academic.live.Notify(ctx, EventFinanceChanged, map[string]string{"institution": sc.institution.Slug, "area": area, "student": studentID})
}

func (s *FinanceService) name(sc financeScope) string {
	if sc.userName != "" {
		return sc.userName
	}
	return sc.email
}

func (s *FinanceService) today() time.Time { return s.assessment.attendance.today() }

// public reports whether the institution is of public management (no tuition: enrollment and the APAFA fee only)
func (s *FinanceService) public(ctx context.Context, institutionID string) (bool, error) {
	st, err := s.assessment.academic.repo.Settings(ctx, institutionID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	return err == nil && ManagementOf(st) == domain.ManagementPublic, err
}

// FormatSoles writes S/ 1 234,50
func FormatSoles(m domain.Money) string {
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	whole := fmt.Sprintf("%d", int64(m)/100)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%sS/ %s,%02d", sign, b.String(), int64(m)%100)
}

// ---------- overview ----------

type MonthTotal struct {
	Month     string // YYYY-MM of the due date
	Expected  domain.Money
	Collected domain.Money
}

type FinanceSummary struct {
	Periods   []domain.Period
	Period    domain.Period
	Expected  domain.Money
	Discounts domain.Money
	Collected domain.Money
	Pending   domain.Money
	Overdue   domain.Money
	Debtors   int
	Students  int
	Months    []MonthTotal
	Accounts  []domain.Account
	CanManage bool
	Today     time.Time
}

func (s *FinanceService) Summary(ctx context.Context, token, slug, periodID string) (FinanceSummary, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return FinanceSummary{}, err
	}
	if sc.family() {
		return FinanceSummary{}, domain.ErrFinanceForbidden
	}
	p, periods, err := s.assessment.period(ctx, gradeScope{attendanceScope: sc.attendanceScope}, periodID)
	if err != nil {
		return FinanceSummary{}, err
	}
	out := FinanceSummary{Periods: periods, Period: p, CanManage: sc.manager(), Today: s.today()}
	if p.ID == "" {
		return out, nil
	}
	if out.Accounts, err = s.repo.Accounts(ctx, sc.institution.ID, p.ID, out.Today); err != nil {
		return out, err
	}
	for _, a := range out.Accounts {
		out.Expected += a.Charged
		out.Discounts += a.Discounts
		out.Collected += a.Paid
		out.Overdue += a.Overdue
		if a.Overdue > 0 {
			out.Debtors++
		}
	}
	out.Students = len(out.Accounts)
	out.Pending = out.Expected - out.Discounts - out.Collected
	// By due month: what was due and what of it is paid
	months := map[string]*MonthTotal{}
	for _, a := range out.Accounts {
		charges, err := s.repo.Charges(ctx, sc.institution.ID, a.StudentID)
		if err != nil {
			return out, err
		}
		for _, c := range charges {
			if c.PeriodID != p.ID || c.Status == "void" {
				continue
			}
			k := c.DueOn.Format("2006-01")
			if months[k] == nil {
				months[k] = &MonthTotal{Month: k}
			}
			months[k].Expected += c.Amount - c.Discount
			months[k].Collected += c.Paid
		}
	}
	for _, m := range months {
		out.Months = append(out.Months, *m)
	}
	sort.Slice(out.Months, func(i, j int) bool { return out.Months[i].Month < out.Months[j].Month })
	return out, nil
}

// ---------- plans ----------

func (s *FinanceService) Plans(ctx context.Context, token, slug, periodID string) ([]domain.FeePlan, bool, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, false, err
	}
	if sc.family() {
		return nil, false, domain.ErrFinanceForbidden
	}
	plans, err := s.repo.Plans(ctx, sc.institution.ID, periodID)
	return plans, sc.manager(), err
}

func (s *FinanceService) SavePlan(ctx context.Context, token, slug string, p domain.FeePlan) (domain.FeePlan, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.FeePlan{}, err
	}
	fields := map[string]string{}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len([]rune(p.Name)) > 120 {
		fields["name"] = "Ponle un nombre al plan."
	}
	if len(p.Items) == 0 || len(p.Items) > 24 {
		fields["items"] = "Un plan tiene de 1 a 24 cuotas."
	}
	var period domain.Period
	if p.ID == "" {
		if period, err = s.assessment.academic.repo.Period(ctx, sc.institution.ID, p.PeriodID); err != nil {
			return domain.FeePlan{}, err
		}
	} else {
		cur, err := s.repo.Plan(ctx, sc.institution.ID, p.ID)
		if err != nil {
			return domain.FeePlan{}, err
		}
		if period, err = s.assessment.academic.repo.Period(ctx, sc.institution.ID, cur.PeriodID); err != nil {
			return domain.FeePlan{}, err
		}
	}
	public, err := s.public(ctx, sc.institution.ID)
	if err != nil {
		return domain.FeePlan{}, err
	}
	for i := range p.Items {
		it := &p.Items[i]
		it.Name = strings.TrimSpace(it.Name)
		switch {
		case !slices.Contains(feeConcepts, it.Concept):
			fields["items"] = "Cada cuota es matrícula, pensión, cuota APAFA u otro concepto."
		case public && it.Concept == "tuition":
			fields["items"] = noTuitionMessage
		case it.Name == "" || len([]rune(it.Name)) > 120:
			fields["items"] = "Cada cuota necesita un nombre."
		case it.Amount <= 0 || it.Amount > 10_000_000:
			fields["items"] = "Los montos van de S/ 0,01 a S/ 100 000."
		case it.DueOn.IsZero() || it.DueOn.Before(period.StartsOn.AddDate(0, -3, 0)) || it.DueOn.After(period.EndsOn.AddDate(0, 3, 0)):
			fields["items"] = "Los vencimientos deben estar cerca de las fechas del periodo."
		}
	}
	if len(fields) > 0 {
		return domain.FeePlan{}, domain.Validation(fields)
	}
	out, err := s.repo.SavePlan(ctx, sc.institution.ID, p)
	if err == nil {
		s.changed(ctx, sc, "plans", "")
	}
	return out, err
}

func (s *FinanceService) DeletePlan(ctx context.Context, token, slug, id string) error {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return err
	}
	p, err := s.repo.Plan(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if p.Students > 0 {
		return domain.Validation(map[string]string{"plan": "El plan ya generó cronogramas: no se elimina."})
	}
	if err := s.repo.DeletePlan(ctx, sc.institution.ID, id); err != nil {
		return err
	}
	s.changed(ctx, sc, "plans", "")
	return nil
}

// Generate gives every enrolled student of the plan's period (and level) the charges they still lack
func (s *FinanceService) Generate(ctx context.Context, token, slug, id string) (int, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return 0, err
	}
	// A plan from before the institution turned public may still list tuition: it does not generate it
	public, err := s.public(ctx, sc.institution.ID)
	if err != nil {
		return 0, err
	}
	if public {
		p, err := s.repo.Plan(ctx, sc.institution.ID, id)
		if err != nil {
			return 0, err
		}
		if slices.ContainsFunc(p.Items, func(it domain.FeeItem) bool { return it.Concept == "tuition" }) {
			return 0, domain.Validation(map[string]string{"plan": noTuitionMessage})
		}
	}
	n, err := s.repo.GenerateCharges(ctx, sc.institution.ID, id)
	if err == nil && n > 0 {
		s.changed(ctx, sc, "plans", "")
	}
	return n, err
}

// ---------- a student's account ----------

type Statement struct {
	Student   domain.CardHolder
	Charges   []domain.Charge
	Payments  []domain.Payment
	Discounts []domain.Discount
	Reminders []domain.Reminder
	Balance   domain.Money
	Overdue   domain.Money
	CanManage bool
	Today     time.Time
}

func (s *FinanceService) Statement(ctx context.Context, token, slug, studentID string) (Statement, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return Statement{}, err
	}
	if sc.family() {
		mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return Statement{}, err
		}
		if !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == studentID }) {
			return Statement{}, domain.ErrNotYourStudent
		}
	}
	return s.statement(ctx, sc, studentID)
}

func (s *FinanceService) statement(ctx context.Context, sc financeScope, studentID string) (Statement, error) {
	name, code, err := s.repo.StudentName(ctx, sc.institution.ID, studentID)
	if err != nil {
		return Statement{}, err
	}
	out := Statement{Student: domain.CardHolder{Kind: "student", ID: studentID, Name: name, Code: code}, CanManage: sc.manager(), Today: s.today()}
	if out.Charges, err = s.repo.Charges(ctx, sc.institution.ID, studentID); err != nil {
		return out, err
	}
	if out.Payments, err = s.repo.Payments(ctx, domain.PaymentFilter{InstitutionID: sc.institution.ID, StudentID: studentID, Limit: 100}); err != nil {
		return out, err
	}
	if out.Discounts, err = s.repo.Discounts(ctx, sc.institution.ID, "", studentID); err != nil {
		return out, err
	}
	if out.Reminders, err = s.repo.Reminders(ctx, sc.institution.ID, studentID); err != nil {
		return out, err
	}
	for _, c := range out.Charges {
		out.Balance += c.Balance()
		if c.DueOn.Before(out.Today) {
			out.Overdue += c.Balance()
		}
	}
	return out, nil
}

// MyStatements: each child of the family (or the adult student)
func (s *FinanceService) Mine(ctx context.Context, token, slug string) ([]Statement, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return nil, err
	}
	if !sc.family() {
		return nil, nil
	}
	mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
	if err != nil {
		return nil, err
	}
	var out []Statement
	for _, h := range mine {
		st, err := s.statement(ctx, sc, h.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// Allocate settles the payment against the chosen charges, or the oldest pending ones; it fails when it pays more
// than what is owed
func Allocate(charges []domain.Charge, amount domain.Money, chosen []string) ([]domain.Allocation, error) {
	pending := make([]domain.Charge, 0, len(charges))
	for _, c := range charges {
		if c.Balance() > 0 && (len(chosen) == 0 || slices.Contains(chosen, c.ID)) {
			pending = append(pending, c)
		}
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].DueOn.Before(pending[j].DueOn) })
	if len(pending) == 0 {
		return nil, domain.ErrNothingOwed
	}
	var out []domain.Allocation
	left := amount
	for _, c := range pending {
		if left == 0 {
			break
		}
		take := min(left, c.Balance())
		out = append(out, domain.Allocation{ChargeID: c.ID, ChargeName: c.Name, Amount: take})
		left -= take
	}
	if left > 0 {
		return nil, domain.ErrOverpayment
	}
	return out, nil
}

type PaymentInput struct {
	StudentID string
	PaidOn    time.Time
	Amount    domain.Money
	Method    string
	Reference string
	Payer     string
	Notes     string
	ChargeIDs []string
}

func (s *FinanceService) RegisterPayment(ctx context.Context, token, slug string, in PaymentInput) (domain.Payment, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return domain.Payment{}, err
	}
	in.Reference, in.Payer, in.Notes = strings.TrimSpace(in.Reference), strings.TrimSpace(in.Payer), strings.TrimSpace(in.Notes)
	fields := map[string]string{}
	if in.Amount <= 0 {
		fields["amount"] = "Indica el monto pagado."
	}
	if !slices.Contains(paymentMethods, in.Method) {
		fields["method"] = "Elige cómo pagó."
	}
	if in.Method != "cash" && in.Reference == "" {
		fields["reference"] = "Anota el número de operación."
	}
	today := s.today()
	if in.PaidOn.IsZero() {
		in.PaidOn = today
	}
	if in.PaidOn.After(today) || in.PaidOn.Before(today.AddDate(-1, 0, 0)) {
		fields["paidOn"] = "La fecha del pago no puede ser futura ni de hace más de un año."
	}
	if len([]rune(in.Reference)) > 80 || len([]rune(in.Payer)) > 160 || len([]rune(in.Notes)) > 300 {
		fields["notes"] = "Algún dato es demasiado largo."
	}
	if len(fields) > 0 {
		return domain.Payment{}, domain.Validation(fields)
	}
	charges, err := s.repo.Charges(ctx, sc.institution.ID, in.StudentID)
	if err != nil {
		return domain.Payment{}, err
	}
	allocs, err := Allocate(charges, in.Amount, in.ChargeIDs)
	if err != nil {
		return domain.Payment{}, err
	}
	p, err := s.repo.CreatePayment(ctx, sc.institution.ID, domain.Payment{StudentID: in.StudentID, PaidOn: in.PaidOn, Amount: in.Amount, Method: in.Method, Reference: in.Reference,
		Payer: in.Payer, Notes: in.Notes, ReceivedName: s.name(sc), Allocations: allocs}, sc.userID)
	if err == nil {
		s.changed(ctx, sc, "payments", in.StudentID)
	}
	return p, err
}

func (s *FinanceService) AnnulPayment(ctx context.Context, token, slug, id, reason string) error {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 200 {
		return domain.Validation(map[string]string{"reason": "Explica por qué se anula (hasta 200 caracteres)."})
	}
	p, err := s.repo.Payment(ctx, sc.institution.ID, id)
	if err != nil {
		return err
	}
	if err := s.repo.AnnulPayment(ctx, sc.institution.ID, id, reason); err != nil {
		return err
	}
	s.changed(ctx, sc, "payments", p.StudentID)
	return nil
}

func (s *FinanceService) Payment(ctx context.Context, token, slug, id string) (domain.Payment, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return domain.Payment{}, err
	}
	p, err := s.repo.Payment(ctx, sc.institution.ID, id)
	if err != nil {
		return p, err
	}
	if sc.family() {
		mine, err := s.assessment.attendance.ownStudents(ctx, sc.attendanceScope)
		if err != nil {
			return p, err
		}
		if !slices.ContainsFunc(mine, func(h domain.CardHolder) bool { return h.ID == p.StudentID }) {
			return domain.Payment{}, domain.ErrNotYourStudent
		}
	}
	return p, nil
}

// Cash is the journal of one day (or range): every payment and the totals per method
type Cash struct {
	From, To time.Time
	Payments []domain.Payment
	ByMethod map[string]domain.Money
	Total    domain.Money
	Annulled int
}

func (s *FinanceService) Cash(ctx context.Context, token, slug string, from, to time.Time, method string) (Cash, error) {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return Cash{}, err
	}
	if sc.family() {
		return Cash{}, domain.ErrFinanceForbidden
	}
	if from.IsZero() {
		from = s.today()
	}
	if to.IsZero() || to.Before(from) {
		to = from
	}
	if to.Sub(from) > 92*24*time.Hour {
		return Cash{}, domain.Validation(map[string]string{"to": "Consulta hasta tres meses a la vez."})
	}
	list, err := s.repo.Payments(ctx, domain.PaymentFilter{InstitutionID: sc.institution.ID, From: from, To: to, Method: method, Limit: 2000})
	if err != nil {
		return Cash{}, err
	}
	out := Cash{From: from, To: to, Payments: list, ByMethod: map[string]domain.Money{}}
	for _, p := range list {
		if p.Status != "valid" {
			out.Annulled++
			continue
		}
		out.ByMethod[p.Method] += p.Amount
		out.Total += p.Amount
	}
	return out, nil
}

// ---------- discounts and charges ----------

func (s *FinanceService) SaveDiscount(ctx context.Context, token, slug string, d domain.Discount) (Statement, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return Statement{}, err
	}
	d.Reason = strings.TrimSpace(d.Reason)
	fields := map[string]string{}
	if d.Percent <= 0 || d.Percent > 100 {
		fields["percent"] = "El descuento va de 1 a 100 %."
	}
	if d.Reason == "" || len([]rune(d.Reason)) > 200 {
		fields["reason"] = "Indica el motivo: beca, hermanos, convenio…"
	}
	if len(fields) > 0 {
		return Statement{}, domain.Validation(fields)
	}
	if _, err := s.assessment.academic.repo.Period(ctx, sc.institution.ID, d.PeriodID); err != nil {
		return Statement{}, err
	}
	// Without tuition a "tuition only" discount would apply to nothing: in a public institution it covers every
	// charge (an exoneration of the enrollment and the APAFA fee)
	public, err := s.public(ctx, sc.institution.ID)
	if err != nil {
		return Statement{}, err
	}
	if public {
		d.TuitionOnly = false
	}
	if err := s.repo.SaveDiscount(ctx, sc.institution.ID, d); err != nil {
		return Statement{}, err
	}
	if err := s.repo.ApplyDiscount(ctx, sc.institution.ID, d.StudentID, d.PeriodID); err != nil {
		return Statement{}, err
	}
	s.changed(ctx, sc, "discounts", d.StudentID)
	return s.statement(ctx, sc, d.StudentID)
}

func (s *FinanceService) DeleteDiscount(ctx context.Context, token, slug, studentID, periodID string) (Statement, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return Statement{}, err
	}
	if err := s.repo.DeleteDiscount(ctx, sc.institution.ID, studentID, periodID); err != nil {
		return Statement{}, err
	}
	if err := s.repo.ApplyDiscount(ctx, sc.institution.ID, studentID, periodID); err != nil {
		return Statement{}, err
	}
	s.changed(ctx, sc, "discounts", studentID)
	return s.statement(ctx, sc, studentID)
}

func (s *FinanceService) VoidCharge(ctx context.Context, token, slug, id, reason string) (Statement, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return Statement{}, err
	}
	c, err := s.repo.Charge(ctx, sc.institution.ID, id)
	if err != nil {
		return Statement{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 200 {
		return Statement{}, domain.Validation(map[string]string{"reason": "Explica por qué se anula la cuota."})
	}
	if c.Paid > 0 {
		return Statement{}, domain.ErrChargePaid
	}
	if err := s.repo.VoidCharge(ctx, sc.institution.ID, id, reason); err != nil {
		return Statement{}, err
	}
	s.changed(ctx, sc, "payments", c.StudentID)
	return s.statement(ctx, sc, c.StudentID)
}

func (s *FinanceService) AddCharge(ctx context.Context, token, slug string, c domain.Charge) (Statement, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return Statement{}, err
	}
	c.Name = strings.TrimSpace(c.Name)
	fields := map[string]string{}
	if c.Name == "" || len([]rune(c.Name)) > 120 {
		fields["name"] = "Indica el concepto (por ejemplo, Carné de biblioteca)."
	}
	if c.Amount <= 0 || c.Amount > 10_000_000 {
		fields["amount"] = "Indica el monto."
	}
	if c.DueOn.IsZero() {
		fields["dueOn"] = "Indica el vencimiento."
	}
	if len(fields) > 0 {
		return Statement{}, domain.Validation(fields)
	}
	if _, err := s.assessment.academic.repo.Period(ctx, sc.institution.ID, c.PeriodID); err != nil {
		return Statement{}, err
	}
	c.Concept = "other"
	if _, err := s.repo.AddCharge(ctx, sc.institution.ID, c); err != nil {
		return Statement{}, err
	}
	s.changed(ctx, sc, "payments", c.StudentID)
	return s.statement(ctx, sc, c.StudentID)
}

// ---------- reminders ----------

// Remind leaves a message in the panel of each family with overdue charges (or the chosen students)
func (s *FinanceService) Remind(ctx context.Context, token, slug, periodID string, studentIDs []string) (int, error) {
	sc, err := s.manage(ctx, token, slug)
	if err != nil {
		return 0, err
	}
	accounts, err := s.repo.Accounts(ctx, sc.institution.ID, periodID, s.today())
	if err != nil {
		return 0, err
	}
	var items []domain.Reminder
	for _, a := range accounts {
		if a.Overdue <= 0 || (len(studentIDs) > 0 && !slices.Contains(studentIDs, a.StudentID)) {
			continue
		}
		msg := fmt.Sprintf("Estimada familia: el estado de cuenta de %s tiene %d %s vencidas por %s. Puede acercarse a tesorería o pagar por transferencia, Yape o Plin. Si ya pagó, no considere este mensaje.",
			displayName(a.Name), a.OverdueCount, plural(a.OverdueCount, "cuota", "cuotas"), FormatSoles(a.Overdue))
		items = append(items, domain.Reminder{StudentID: a.StudentID, Message: msg, Overdue: a.Overdue, SentName: s.name(sc)})
	}
	if len(items) == 0 {
		return 0, nil
	}
	if err := s.repo.AddReminders(ctx, sc.institution.ID, items); err != nil {
		return 0, err
	}
	s.changed(ctx, sc, "reminders", "")
	return len(items), nil
}

// InstitutionName for the receipts (empty when the session cannot open the institution)
func (s *FinanceService) InstitutionName(ctx context.Context, token, slug string) string {
	sc, err := s.scope(ctx, token, slug)
	if err != nil {
		return ""
	}
	return sc.institution.Name
}
