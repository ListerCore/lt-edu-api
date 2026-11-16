package service

import (
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestValidateItemByKind(t *testing.T) {
	start := time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)
	hour := 60
	tooShort := 2
	cases := []struct {
		name  string
		in    domain.Item
		field string
	}{
		{"reading with text", domain.Item{Kind: "reading", Title: "Lectura 1", Body: "Texto"}, ""},
		{"reading needs text", domain.Item{Kind: "reading", Title: "Lectura 1"}, "body"},
		{"reading from library", domain.Item{Kind: "reading", Title: "Lectura 1", LibraryItemID: "x"}, ""},
		{"activity needs instructions", domain.Item{Kind: "activity", Title: "Tarea"}, "body"},
		{"file needs nothing more", domain.Item{Kind: "file", Title: "Guía"}, ""},
		{"video needs a link", domain.Item{Kind: "video", Title: "Video"}, "url"},
		{"only https", domain.Item{Kind: "link", Title: "Sitio", URL: "http://minedu.gob.pe"}, "url"},
		{"live complete", domain.Item{Kind: "live", Title: "Sesión", URL: "https://meet.google.com/abc", StartsAt: &start, DurationMinutes: &hour}, ""},
		{"live needs a link", domain.Item{Kind: "live", Title: "Sesión", StartsAt: &start, DurationMinutes: &hour}, "url"},
		{"live needs a start", domain.Item{Kind: "live", Title: "Sesión", URL: "https://meet.google.com/abc", DurationMinutes: &hour}, "startsAt"},
		{"live length", domain.Item{Kind: "live", Title: "Sesión", URL: "https://meet.google.com/abc", StartsAt: &start, DurationMinutes: &tooShort}, "durationMinutes"},
		{"title required", domain.Item{Kind: "file", Title: " "}, "title"},
		{"unknown kind", domain.Item{Kind: "quiz", Title: "Examen"}, "kind"},
	}
	for _, c := range cases {
		in := c.in
		fields := validateItem(&in)
		if c.field == "" && len(fields) > 0 {
			t.Errorf("%s: unexpected %v", c.name, fields)
		}
		if c.field != "" && fields[c.field] == "" {
			t.Errorf("%s: want error on %s, got %v", c.name, c.field, fields)
		}
	}
	// Session data only stays on live sessions
	link := domain.Item{Kind: "link", Title: "Sitio", URL: "https://minedu.gob.pe", StartsAt: &start, DurationMinutes: &hour, RecordingURL: "https://x.pe"}
	validateItem(&link)
	if link.StartsAt != nil || link.DurationMinutes != nil || link.RecordingURL != "" {
		t.Fatal("a link kept live session fields")
	}
}

func TestVisibleUnitsForStudents(t *testing.T) {
	today := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	later := today.AddDate(0, 0, 7)
	units := []domain.Unit{
		{ID: "open", Published: true, Items: []domain.Item{{ID: "a", Published: true}, {ID: "draft", Published: false}}},
		{ID: "draft-unit", Published: false, Items: []domain.Item{{ID: "b", Published: true}}},
		{ID: "next-week", Published: true, OpensOn: &later, Items: []domain.Item{{ID: "c", Published: true}}},
		{ID: "opens-today", Published: true, OpensOn: &today, Items: []domain.Item{}},
	}
	got := visibleUnits(units, today)
	if len(got) != 2 || got[0].ID != "open" || got[1].ID != "opens-today" {
		t.Fatalf("units: %+v", got)
	}
	if len(got[0].Items) != 1 || got[0].Items[0].ID != "a" {
		t.Fatalf("items: %+v", got[0].Items)
	}
}

func TestValidateLibraryItem(t *testing.T) {
	ok := domain.LibraryItem{Kind: "video", Title: "  Fracciones  ", URL: "https://www.youtube.com/watch?v=x", Topic: "Matemática"}
	if f := validateLibraryItem(&ok); len(f) > 0 || ok.Title != "Fracciones" {
		t.Fatalf("valid: %v %q", f, ok.Title)
	}
	for name, c := range map[string]struct {
		in    domain.LibraryItem
		field string
	}{
		"live is not a library kind": {domain.LibraryItem{Kind: "live", Title: "Sesión"}, "kind"},
		"reading needs text":         {domain.LibraryItem{Kind: "reading", Title: "Lectura"}, "body"},
		"link needs an address":      {domain.LibraryItem{Kind: "link", Title: "Sitio"}, "url"},
	} {
		in := c.in
		if f := validateLibraryItem(&in); f[c.field] == "" {
			t.Errorf("%s: want error on %s, got %v", name, c.field, f)
		}
	}
}
