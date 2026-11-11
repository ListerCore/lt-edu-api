package service

import (
	"errors"
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestAllocate(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC) }
	charges := []domain.Charge{
		{ID: "apr", Amount: 42000, DueOn: d(30), Status: "pending"},
		{ID: "mar", Amount: 42000, Discount: 4200, Paid: 10000, DueOn: d(1), Status: "pending"},
		{ID: "void", Amount: 5000, DueOn: d(2), Status: "void"},
	}
	// 30 000 left in March (37 800 − 10 000 = 27 800) then April
	got, err := Allocate(charges, 50000, nil)
	if err != nil || len(got) != 2 || got[0].ChargeID != "mar" || got[0].Amount != 27800 || got[1].Amount != 22200 {
		t.Fatalf("got %+v %v", got, err)
	}
	if _, err := Allocate(charges, 80000, nil); !errors.Is(err, domain.ErrOverpayment) {
		t.Fatalf("overpayment accepted: %v", err)
	}
	if got, err := Allocate(charges, 1000, []string{"apr"}); err != nil || got[0].ChargeID != "apr" {
		t.Fatalf("chosen charge: %+v %v", got, err)
	}
	if _, err := Allocate([]domain.Charge{{ID: "x", Amount: 100, Paid: 100, Status: "pending"}}, 10, nil); !errors.Is(err, domain.ErrNothingOwed) {
		t.Fatalf("nothing owed: %v", err)
	}
}

func TestFormatSoles(t *testing.T) {
	for m, want := range map[domain.Money]string{0: "S/ 0,00", 42050: "S/ 420,50", 123456789: "S/ 1 234 567,89", -500: "-S/ 5,00"} {
		if got := FormatSoles(m); got != want {
			t.Errorf("%d: got %q want %q", m, got, want)
		}
	}
}
