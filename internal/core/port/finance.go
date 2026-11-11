package port

import (
	"context"
	"time"

	"api/internal/core/domain"
)

// FinanceRepository is the finance schema in PostgreSQL, scoped to institutionID
type FinanceRepository interface {
	Plans(ctx context.Context, institutionID, periodID string) ([]domain.FeePlan, error)
	Plan(ctx context.Context, institutionID, id string) (domain.FeePlan, error)
	// SavePlan creates or updates a plan and its items (items keep their ids; removed ones are deleted)
	SavePlan(ctx context.Context, institutionID string, p domain.FeePlan) (domain.FeePlan, error)
	DeletePlan(ctx context.Context, institutionID, id string) error
	// GenerateCharges gives every student enrolled (active) in the plan's period and level the missing charges,
	// with their discount; returns how many were created
	GenerateCharges(ctx context.Context, institutionID, planID string) (int, error)

	Discounts(ctx context.Context, institutionID, periodID, studentID string) ([]domain.Discount, error)
	SaveDiscount(ctx context.Context, institutionID string, d domain.Discount) error
	DeleteDiscount(ctx context.Context, institutionID, studentID, periodID string) error
	// ApplyDiscount recomputes the discount of the student's charges of the period that have no payments yet
	ApplyDiscount(ctx context.Context, institutionID, studentID, periodID string) error

	Charges(ctx context.Context, institutionID, studentID string) ([]domain.Charge, error)
	Charge(ctx context.Context, institutionID, id string) (domain.Charge, error)
	VoidCharge(ctx context.Context, institutionID, id, reason string) error
	AddCharge(ctx context.Context, institutionID string, c domain.Charge) (domain.Charge, error)

	CreatePayment(ctx context.Context, institutionID string, p domain.Payment, userID string) (domain.Payment, error)
	Payment(ctx context.Context, institutionID, id string) (domain.Payment, error)
	Payments(ctx context.Context, f domain.PaymentFilter) ([]domain.Payment, error)
	AnnulPayment(ctx context.Context, institutionID, id, reason string) error

	Accounts(ctx context.Context, institutionID, periodID string, today time.Time) ([]domain.Account, error)
	Reminders(ctx context.Context, institutionID, studentID string) ([]domain.Reminder, error)
	AddReminders(ctx context.Context, institutionID string, items []domain.Reminder) error
	// StudentName for receipts and reminders
	StudentName(ctx context.Context, institutionID, studentID string) (string, string, error)
}
