package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestUptimeSummary(t *testing.T) {
	ms := func(v int) *int { return &v }
	list := []domain.UptimeCheck{{OK: true, ResponseMs: ms(100), CheckedAt: time.Now()}, {OK: false, Error: "timeout"}, {OK: true, ResponseMs: ms(300)}, {OK: true, ResponseMs: ms(200)}}
	s := summarize("https://ceba.example", list)
	if s.Checks != 4 || s.Up != 3 || s.Percent != 75 || s.AvgMs != 200 || s.Last == nil || !s.Last.OK || len(s.Recent) != 4 {
		t.Fatalf("summary: %+v", s)
	}
	if e := summarize("", nil); e.Checks != 0 || e.Percent != 0 || e.Last != nil {
		t.Fatalf("empty: %+v", e)
	}
}

func TestItemsDone(t *testing.T) {
	if !itemsDone([]domain.ComplianceItem{{Status: domain.ItemVerified}, {Status: domain.ItemNotApplicable}}) {
		t.Fatal("verified and not applicable are done")
	}
	if itemsDone([]domain.ComplianceItem{{Status: domain.ItemVerified}, {Status: domain.ItemPending}}) {
		t.Fatal("a pending row keeps the draft")
	}
}

func TestEvidenceRef(t *testing.T) {
	cases := []struct {
		it   domain.ComplianceItem
		want string
	}{
		{domain.ComplianceItem{Annex: "03", Folio: "12"}, "Anexo N.° 03 – Folio 12"},
		{domain.ComplianceItem{Annex: "04"}, "Anexo N.° 04"},
		{domain.ComplianceItem{Files: 2}, "2 capturas"},
		{domain.ComplianceItem{}, "—"},
	}
	for _, c := range cases {
		if got := evidenceRef(c.it); got != c.want {
			t.Errorf("%+v: %s", c.it, got)
		}
	}
}

func TestDefaultItems(t *testing.T) {
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, it := range defaultItems {
		counts[it.Report+"/"+it.Section]++
		key := it.Report + "/" + it.Code
		if seen[key] {
			t.Fatalf("duplicate %s", key)
		}
		seen[key] = true
	}
	// Anexo 2: five informative elements and seven of the virtual classroom; 6.3.2 c: six aspects
	if counts["platform/informative"] != 5 || counts["platform/classroom"] != 7 || counts["equipment/equipment"] != 6 {
		t.Fatalf("counts: %v", counts)
	}
}
