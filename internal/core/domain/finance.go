package domain

import "time"

// Money is an amount in céntimos of sol, so sums never round
type Money int64

type FeeItem struct {
	ID       string
	Concept  string
	Name     string
	Amount   Money
	DueOn    time.Time
	Position int
}

type FeePlan struct {
	ID         string
	PeriodID   string
	PeriodName string
	LevelID    string
	LevelName  string
	Name       string
	Items      []FeeItem
	// Students with a schedule from this plan
	Students int
}

type Discount struct {
	ID          string
	StudentID   string
	StudentName string
	PeriodID    string
	Percent     float64
	TuitionOnly bool
	Reason      string
}

type Charge struct {
	ID         string
	StudentID  string
	PeriodID   string
	PeriodName string
	ItemID     string
	Concept    string
	Name       string
	Amount     Money
	Discount   Money
	Paid       Money
	DueOn      time.Time
	Status     string
	VoidReason string
}

// Balance is what is still owed of the charge
func (c Charge) Balance() Money {
	if c.Status == "void" {
		return 0
	}
	return c.Amount - c.Discount - c.Paid
}

type Allocation struct {
	ChargeID   string
	ChargeName string
	Amount     Money
}

type Payment struct {
	ID           string
	StudentID    string
	StudentName  string
	StudentCode  string
	Number       int
	Receipt      string
	PaidOn       time.Time
	Amount       Money
	Method       string
	Reference    string
	Payer        string
	Notes        string
	Status       string
	AnnulReason  string
	ReceivedName string
	Allocations  []Allocation
}

type PaymentFilter struct {
	InstitutionID string
	StudentID     string
	From, To      time.Time
	Method        string
	Limit         int
}

type Reminder struct {
	ID        string
	StudentID string
	Message   string
	Overdue   Money
	SentName  string
	SentAt    time.Time
}

// Account is a student's position in a period: what was charged, paid and is overdue
type Account struct {
	StudentID    string
	Code         string
	Name         string
	Placement    string
	LevelID      string
	Charged      Money
	Discounts    Money
	Paid         Money
	Overdue      Money
	OverdueCount int
	OldestDue    *time.Time
	LastReminder *time.Time
}

var (
	ErrFinanceForbidden = newError(KindForbidden, "finance_forbidden", "Tu rol no puede ver las pensiones.")
	ErrFinanceReadOnly  = newError(KindForbidden, "finance_read_only", "Solo tesorería y dirección registran pagos y cronogramas.")
	ErrOverpayment      = newError(KindInvalid, "overpayment", "El pago es mayor que la deuda pendiente.")
	ErrNothingOwed      = newError(KindConflict, "nothing_owed", "No tiene cuotas pendientes.")
	ErrPaymentAnnulled  = newError(KindConflict, "payment_annulled", "El pago ya fue anulado.")
	ErrChargePaid       = newError(KindConflict, "charge_paid", "La cuota ya tiene pagos: anula primero los pagos.")
)
