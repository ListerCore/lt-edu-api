package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestValidateLiveItemProvider(t *testing.T) {
	start := time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)
	hour := 60
	cases := []struct {
		name     string
		in       domain.Item
		field    string
		provider string
		url      string
	}{
		{"meet needs a link", domain.Item{Kind: "live", Title: "Clase", Provider: "meet", StartsAt: &start, DurationMinutes: &hour}, "url", "meet", ""},
		{"meet with link", domain.Item{Kind: "live", Title: "Clase", Provider: "meet", URL: "https://meet.google.com/abc-defg-hij", StartsAt: &start, DurationMinutes: &hour}, "", "meet", "https://meet.google.com/abc-defg-hij"},
		{"empty provider is meet", domain.Item{Kind: "live", Title: "Clase", URL: "https://meet.google.com/abc", StartsAt: &start, DurationMinutes: &hour}, "", "meet", "https://meet.google.com/abc"},
		{"platform needs no link and drops it", domain.Item{Kind: "live", Title: "Clase", Provider: "platform", URL: "https://x.test/y", StartsAt: &start, DurationMinutes: &hour}, "", "platform", ""},
		{"platform ignores a bad link", domain.Item{Kind: "live", Title: "Clase", Provider: "platform", URL: "nope", StartsAt: &start, DurationMinutes: &hour}, "", "platform", ""},
		{"unknown provider", domain.Item{Kind: "live", Title: "Clase", Provider: "zoom", URL: "https://x.test", StartsAt: &start, DurationMinutes: &hour}, "provider", "zoom", "https://x.test"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			fields := validateItem(&in)
			if c.field == "" && len(fields) > 0 {
				t.Fatalf("unexpected errors %v", fields)
			}
			if c.field != "" && fields[c.field] == "" {
				t.Fatalf("want error on %s, got %v", c.field, fields)
			}
			if in.Provider != c.provider || in.URL != c.url {
				t.Fatalf("provider/url = %q %q, want %q %q", in.Provider, in.URL, c.provider, c.url)
			}
		})
	}
}

func TestLiveJoinWindow(t *testing.T) {
	start := time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)
	session := domain.LiveTarget{StartsAt: start, DurationMin: 60}
	cases := []struct {
		name string
		host bool
		cxl  bool
		at   time.Time
		want bool
	}{
		{"too early", false, false, start.Add(-16 * time.Minute), false},
		{"15 minutes before", false, false, start.Add(-15 * time.Minute), true},
		{"during", false, false, start.Add(30 * time.Minute), true},
		{"30 minutes after the end", false, false, start.Add(90 * time.Minute), true},
		{"too late", false, false, start.Add(91 * time.Minute), false},
		{"host the day before", true, false, start.Add(-24 * time.Hour), true},
		{"cancelled, even the host", true, true, start, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := session
			s.IsHost, s.Cancelled = c.host, c.cxl
			if got := s.CanJoinAt(c.at); got != c.want {
				t.Fatalf("CanJoinAt = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLiveKitToken(t *testing.T) {
	cfg := LiveKitConfig{URL: "wss://live.test", APIKey: "devkey", APISecret: "secret"}
	now := time.Date(2026, 11, 20, 18, 50, 0, 0, time.UTC)
	cases := []struct {
		name    string
		host    bool
		ends    time.Time
		wantExp time.Time
	}{
		{"an hour after the end", false, now.Add(70 * time.Minute), now.Add(130 * time.Minute)},
		{"capped at six hours", true, now.Add(10 * time.Hour), now.Add(6 * time.Hour)},
		{"already over: one hour", false, now.Add(-3 * time.Hour), now.Add(time.Hour)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok, err := LiveKitToken(cfg, "class-1", "user-1", "Ana Quispe", c.host, now, c.ends)
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(tok, ".")
			if len(parts) != 3 {
				t.Fatalf("not a JWT: %s", tok)
			}
			mac := hmac.New(sha256.New, []byte("secret"))
			mac.Write([]byte(parts[0] + "." + parts[1]))
			if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
				t.Fatal("bad signature")
			}
			raw, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			var cl liveClaims
			if err := json.Unmarshal(raw, &cl); err != nil {
				t.Fatal(err)
			}
			if cl.Iss != "devkey" || cl.Sub != "user-1" || cl.Name != "Ana Quispe" || cl.Video.Room != "class-1" || !cl.Video.RoomJoin || !cl.Video.CanPublish || !cl.Video.CanSubscribe {
				t.Fatalf("claims %+v", cl)
			}
			if cl.Video.RoomAdmin != c.host {
				t.Fatalf("roomAdmin = %v, want %v", cl.Video.RoomAdmin, c.host)
			}
			if cl.Exp != c.wantExp.Unix() {
				t.Fatalf("exp = %v, want %v", time.Unix(cl.Exp, 0).UTC(), c.wantExp)
			}
			if cl.Nbf > now.Unix() {
				t.Fatal("nbf in the future")
			}
		})
	}
}

func TestValidateAppointment(t *testing.T) {
	at := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		in    domain.PsychAppointment
		field string
		link  string
	}{
		{"defaults: virtual on the platform", domain.PsychAppointment{ScheduledAt: at, DurationMin: 45}, "", ""},
		{"meet with link", domain.PsychAppointment{ScheduledAt: at, DurationMin: 45, Provider: "meet", Link: " https://meet.google.com/x "}, "", "https://meet.google.com/x"},
		{"bad link", domain.PsychAppointment{ScheduledAt: at, DurationMin: 45, Provider: "meet", Link: "meet.google.com/x"}, "link", "meet.google.com/x"},
		{"in person drops the link", domain.PsychAppointment{ScheduledAt: at, DurationMin: 45, Mode: "in_person", Provider: "meet", Link: "https://meet.google.com/x"}, "", ""},
		{"no date", domain.PsychAppointment{DurationMin: 45}, "scheduledAt", ""},
		{"too long", domain.PsychAppointment{ScheduledAt: at, DurationMin: 300}, "durationMin", ""},
		{"unknown mode", domain.PsychAppointment{ScheduledAt: at, DurationMin: 45, Mode: "phone"}, "mode", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			errs := ValidateAppointment(&in)
			if c.field == "" && len(errs) > 0 {
				t.Fatalf("unexpected %v", errs)
			}
			if c.field != "" && errs[c.field] == "" {
				t.Fatalf("want %s, got %v", c.field, errs)
			}
			if in.Link != c.link {
				t.Fatalf("link = %q, want %q", in.Link, c.link)
			}
		})
	}
}
