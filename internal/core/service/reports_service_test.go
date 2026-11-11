package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

// TestReportRange: the attendance range defaults to the last ninety days and never leaves the period or passes today
func TestReportRange(t *testing.T) {
	day := func(s string) time.Time { d, _ := time.Parse("2006-01-02", s); return d }
	p := domain.Period{StartsOn: day("2026-03-09"), EndsOn: day("2026-12-18")}
	cases := []struct {
		name, today, from, to, wantFrom, wantTo string
	}{
		{"default", "2026-11-11", "", "", "2026-08-14", "2026-11-11"},
		{"early in the year", "2026-04-01", "", "", "2026-03-09", "2026-04-01"},
		{"closed period", "2027-02-01", "", "", "2026-09-20", "2026-12-18"},
		{"asked range", "2026-11-11", "2026-01-01", "2027-01-01", "2026-03-09", "2026-11-11"},
		{"inverted", "2026-11-11", "2026-10-10", "2026-10-01", "2026-10-01", "2026-10-01"},
	}
	for _, c := range cases {
		var from, to *time.Time
		if c.from != "" {
			f := day(c.from)
			from = &f
		}
		if c.to != "" {
			x := day(c.to)
			to = &x
		}
		gotFrom, gotTo := reportRange(p, day(c.today), from, to)
		if gotFrom.Format("2006-01-02") != c.wantFrom || gotTo.Format("2006-01-02") != c.wantTo {
			t.Errorf("%s: got %s..%s, want %s..%s", c.name, gotFrom.Format("2006-01-02"), gotTo.Format("2006-01-02"), c.wantFrom, c.wantTo)
		}
	}
}

// TestStatusRate: present and late count as attended; no marks is -1
func TestStatusRate(t *testing.T) {
	if r := (domain.StatusCount{}).Rate(); r != -1 {
		t.Errorf("empty rate = %v", r)
	}
	if r := (domain.StatusCount{Present: 6, Late: 2, Absent: 1, Justified: 1}).Rate(); r != 80 {
		t.Errorf("rate = %v, want 80", r)
	}
}
