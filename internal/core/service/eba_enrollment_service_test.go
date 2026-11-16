package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestEquivalentEntry(t *testing.T) {
	cases := []struct {
		origin  string
		passed  int
		level   string
		grade   int
		done    bool
		unknown bool
	}{
		{domain.OriginEBRPrimary, 0, "inicial", 0, false, false},
		{domain.OriginEBRPrimary, 2, "intermedio", 0, false, false},
		{domain.OriginEBRPrimary, 4, "intermedio", 2, false, false},
		{domain.OriginEBRPrimary, 5, "intermedio", 2, false, false},
		{domain.OriginEBRPrimary, 6, "avanzado", 0, false, false},
		{domain.OriginEBRSecondary, 2, "avanzado", 2, false, false},
		{domain.OriginEBRSecondary, 4, "avanzado", 3, false, false},
		{domain.OriginEBRSecondary, 5, "", 0, true, false},
		{"eda", 3, "", 0, false, true},
	}
	for _, c := range cases {
		level, grade, done, ok := domain.EquivalentEntry(c.origin, c.passed)
		if ok == c.unknown || level != c.level || grade != c.grade || done != c.done {
			t.Errorf("%s %d: got %s %d done=%v ok=%v", c.origin, c.passed, level, grade, done, ok)
		}
	}
}

func TestAgeOn(t *testing.T) {
	birth := time.Date(2012, 3, 20, 0, 0, 0, 0, time.UTC)
	if a := domain.AgeOn(birth, time.Date(2026, 3, 19, 0, 0, 0, 0, time.UTC)); a != 13 {
		t.Fatalf("day before the birthday: %d", a)
	}
	if a := domain.AgeOn(birth, time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)); a != 14 {
		t.Fatalf("on the birthday: %d", a)
	}
}

func TestValidateFile(t *testing.T) {
	f := domain.EnrollmentFile{MotherTongue: "other", Documents: []string{"dni", "dni"}}
	if err := validateFile(&f); err == nil {
		t.Fatal("other tongue without its name accepted")
	}
	f = domain.EnrollmentFile{MotherTongue: "quechua", MotherTongueOther: "x", Occupation: "  Agricultor ", Documents: []string{"dni", "dni"}}
	if err := validateFile(&f); err != nil || f.MotherTongueOther != "" || f.Occupation != "Agricultor" || len(f.Documents) != 1 || f.Internet != "unknown" {
		t.Fatalf("normalized: %+v %v", f, err)
	}
	f = domain.EnrollmentFile{PreferredMode: "distance", Internet: "none", Device: "none"}
	if err := validateFile(&f); err == nil {
		t.Fatal("distance without any connection accepted")
	}
}
