package service

import (
	"strings"
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestLateAfter(t *testing.T) {
	blocks := []domain.TimeBlock{
		{Shift: "morning", StartsAt: "08:00", Kind: "class"}, {Shift: "morning", StartsAt: "07:45", Kind: "break"},
		{Shift: "afternoon", StartsAt: "13:00", Kind: "class"},
	}
	cases := map[string]string{"morning": "08:10", "afternoon": "13:10", "evening": "", "": "08:10"}
	for shift, want := range cases {
		if got := lateAfter(blocks, shift, 10); got != want {
			t.Errorf("lateAfter(%q) = %q, want %q", shift, got, want)
		}
	}
}

func TestSuggestedClassMark(t *testing.T) {
	cases := []struct{ status, day, want string }{
		{"", "", domain.MarkPresent}, {"", domain.MarkLate, domain.MarkPresent}, {"", domain.MarkAbsent, domain.MarkAbsent},
		{"", domain.MarkJustified, domain.MarkJustified}, {domain.MarkLate, domain.MarkAbsent, domain.MarkLate},
	}
	for _, c := range cases {
		if got := suggestedClassMark(domain.ClassMark{Status: c.status, DayStatus: c.day}); got != c.want {
			t.Errorf("suggested(%q, %q) = %q, want %q", c.status, c.day, got, c.want)
		}
	}
}

func TestTallyPercent(t *testing.T) {
	tl := domain.Tally{Present: 6, Late: 1, Absent: 3}
	if tl.Total() != 10 || tl.AbsentPercent() != 30 {
		t.Fatalf("tally %v: total %d, %% %v", tl, tl.Total(), tl.AbsentPercent())
	}
	if (domain.Tally{}).AbsentPercent() != 0 {
		t.Fatal("empty tally must be 0 %")
	}
}

func TestCardCode(t *testing.T) {
	c, err := cardCode("staff", "id", "abcdef0123456789abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c.Payload, cardPrefixStaff) || c.Size < 21 || len(c.Modules) != c.Size*c.Size || strings.Trim(c.Modules, "01") != "" {
		t.Fatalf("bad code %+v", c)
	}
}

func TestTodayInLima(t *testing.T) {
	s := &AttendanceService{now: func() time.Time { return time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC) }}
	if got := s.today().Format(time.DateOnly); got != "2026-11-10" {
		t.Fatalf("03:00 UTC is still the 10th in Lima, got %s", got)
	}
	if isoWeekday(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)) != 7 {
		t.Fatal("Sunday is 7")
	}
}
