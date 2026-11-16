package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestPermanenceWindows(t *testing.T) {
	d := func(m, dd int) time.Time { return time.Date(2026, time.Month(m), dd, 0, 0, 0, 0, time.UTC) }
	rule := domain.PermanenceRule{WindowDays: 7, MinActiveDays: 2, InactiveDays: 14, LowPercent: 60}
	from, today := d(3, 2), d(3, 25) // Mon 2 to Wed 25: windows from 2, 9, 16 and 23 (running)
	active := []time.Time{d(3, 3), d(3, 5), d(3, 10), d(3, 17), d(3, 18), d(3, 23)}
	adj := []domain.PermanenceAdjustment{{WindowStart: d(3, 9), Present: true, Reason: "Trabajó con el cuadernillo"}}

	w, present, counted := PermanenceWindows(rule, from, today, today, active, nil)
	if len(w) != 4 || w[0].ActiveDays != 2 || !w[0].Present || w[1].Present || !w[2].Present || w[3].Present {
		t.Fatalf("windows: %+v", w)
	}
	// The running window (one active day so far) does not count against the student
	if present != 2 || counted != 3 || Percent(present, counted) != 66 {
		t.Fatalf("present %d of %d", present, counted)
	}
	w, present, counted = PermanenceWindows(rule, from, today, today, active, adj)
	if !w[1].Present || !w[1].Adjusted || w[1].Reason == "" || present != 3 || counted != 3 {
		t.Fatalf("adjusted: %+v %d/%d", w[1], present, counted)
	}
	// An adjustment can also take a window away
	_, present, _ = PermanenceWindows(rule, from, today, today, active, []domain.PermanenceAdjustment{{WindowStart: d(3, 2), Present: false, Reason: "x"}})
	if present != 1 {
		t.Fatalf("absent adjustment: %d", present)
	}
	if Percent(0, 0) != 100 {
		t.Fatal("no windows yet is full permanence")
	}
}
