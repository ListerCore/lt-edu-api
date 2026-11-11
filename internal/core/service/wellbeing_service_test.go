package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestAddSchoolDays(t *testing.T) {
	fri := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		from time.Time
		n    int
		want string
	}{
		{fri, 1, "2026-11-09"},
		{fri, 3, "2026-11-11"},
		{fri, 5, "2026-11-13"},
		{fri.AddDate(0, 0, 1), 1, "2026-11-09"},
		{fri, 0, "2026-11-06"},
	}
	for _, c := range cases {
		if got := addSchoolDays(c.from, c.n).Format("2006-01-02"); got != c.want {
			t.Errorf("addSchoolDays(%s, %d) = %s, want %s", c.from.Format("2006-01-02"), c.n, got, c.want)
		}
	}
}

func TestIncidentCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range IncidentCatalog() {
		if seen[c.Code] {
			t.Errorf("duplicate code %s", c.Code)
		}
		seen[c.Code] = true
		if c.Severity != "minor" && c.Severity != "serious" && c.Severity != "very_serious" {
			t.Errorf("%s: bad severity %s", c.Code, c.Severity)
		}
		if c.Crime && c.Severity != "very_serious" {
			t.Errorf("%s: a possible crime must be very serious", c.Code)
		}
	}
	if c, ok := category("bullying"); !ok || !c.Violence {
		t.Error("bullying must go to SíseVe")
	}
	for kind, grounds := range measureGrounds {
		if len(grounds) == 0 {
			t.Errorf("%s without grounds", kind)
		}
	}
}

func TestMeasureCounts(t *testing.T) {
	if !(domain.Measure{Outcome: "upheld"}).Counts() || (domain.Measure{Outcome: "revoked"}).Counts() || !(domain.Measure{}).Counts() {
		t.Error("only a revoked measure stops counting")
	}
}

func TestReferrerView(t *testing.T) {
	now := time.Now()
	c := referrerView(domain.PsychCase{Sessions: 3, LastSession: &now, Status: "open", SharedNote: "x"})
	if c.Sessions != 0 || c.LastSession != nil || c.Status != "open" || c.SharedNote != "x" {
		t.Errorf("referrer view leaks or drops: %+v", c)
	}
}
