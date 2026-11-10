package service

import (
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 secret "12345678901234567890" (8-digit vectors truncated to the last 6 digits)
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"}
	for unix, want := range cases {
		if got := totpCode(secret, totpStep(time.Unix(unix, 0))); got != want {
			t.Errorf("t=%d: code = %s, want %s", unix, got, want)
		}
	}
}

func TestTOTPMatchSkew(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	previous := totpCode(secret, totpStep(now)-1)
	if step, ok := totpMatch(secret, previous, now); !ok || step != totpStep(now)-1 {
		t.Fatal("a code from the previous step must still match")
	}
	if _, ok := totpMatch(secret, totpCode(secret, totpStep(now)-2), now); ok {
		t.Fatal("two steps back is outside the window")
	}
	if _, ok := totpMatch(secret, "12345", now); ok {
		t.Fatal("wrong length never matches")
	}
}
