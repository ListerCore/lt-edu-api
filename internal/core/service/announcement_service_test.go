package service

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"api/internal/core/domain"
)

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	pdfBytes  = []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj<<>>endobj\n")
	webpBytes = append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
)

func TestRandomSlugFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		s := randomSlug()
		if !slugPattern.MatchString(s) {
			t.Fatalf("slug %q is not 18 characters of [a-z0-9]", s)
		}
		seen[s] = true
	}
	if len(seen) != 500 {
		t.Fatal("slugs must not repeat")
	}
}

func TestClassifyFilesByContent(t *testing.T) {
	files := []UploadFile{{FileName: "portada.png", Data: pngBytes}, {FileName: "bases.pdf", Data: pdfBytes}, {FileName: "foto.webp", Data: webpBytes}}
	got, problem := classifyFiles(files, 0, 0)
	if problem != "" {
		t.Fatal(problem)
	}
	want := []struct{ kind, contentType string }{{domain.AttachmentImage, "image/png"}, {domain.AttachmentDocument, "application/pdf"}, {domain.AttachmentImage, "image/webp"}}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].ContentType != w.contentType || got[i].Position != i {
			t.Errorf("file %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestClassifyFilesRejects(t *testing.T) {
	cases := map[string][]UploadFile{
		"renamed executable": {{FileName: "foto.png", Data: []byte("MZ\x90\x00 not an image at all")}},
		"empty":              {{FileName: "vacio.pdf", Data: nil}},
		"image over 5 MB":    {{FileName: "grande.png", Data: append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, 5<<20)...)}},
		"pdf over 10 MB":     {{FileName: "grande.pdf", Data: append(append([]byte{}, pdfBytes...), bytes.Repeat([]byte{0}, 10<<20)...)}},
		"too many":           {{"1.png", pngBytes}, {"2.png", pngBytes}, {"3.png", pngBytes}, {"4.png", pngBytes}, {"5.png", pngBytes}, {"6.png", pngBytes}, {"7.png", pngBytes}},
	}
	for name, files := range cases {
		if _, problem := classifyFiles(files, 0, 0); problem == "" {
			t.Errorf("%s: must be refused", name)
		}
	}
}

func TestCleanFileName(t *testing.T) {
	if got := cleanFileName("../../etc/passwd"); got != "passwd" {
		t.Errorf("path traversal kept: %q", got)
	}
	if got := cleanFileName(`C:\\Users\\ana\\Comunicado.pdf`); got != "Comunicado.pdf" {
		t.Errorf("windows path: %q", got)
	}
	long := strings.Repeat("á", 300) + ".pdf"
	if got := cleanFileName(long); !strings.HasSuffix(got, ".pdf") || len([]rune(got)) != 200 {
		t.Errorf("long name: %d runes", len([]rune(got)))
	}
}

func TestAudiencesByRole(t *testing.T) {
	cases := []struct {
		role, audience string
		can            bool
	}{
		{"director", domain.AudienceStaff, true},
		{"student", domain.AudienceStudents, true},
		{"student", domain.AudienceStaff, false},
		{"student", domain.AudienceGuardians, false},
		{"guardian", domain.AudienceGuardians, true},
		{"guardian", domain.AudienceStudents, false},
		{"teacher", domain.AudienceStaff, true},
		{"teacher", domain.AudienceGuardians, false},
		{"assistant", domain.AudienceCommunity, true},
	}
	for _, c := range cases {
		if got := canRead(c.role, c.audience); got != c.can {
			t.Errorf("%s reading %s = %v, want %v", c.role, c.audience, got, c.can)
		}
	}
	if !announcementPublishers["teacher"] || announcementPublishers["student"] || announcementPublishers["guardian"] {
		t.Error("teachers publish; students and guardians do not")
	}
}

func base() AnnouncementInput {
	return AnnouncementInput{Kind: "notice", Audience: "community", Title: "Matrícula abierta", Body: "Detalle suficientemente largo.", IsPublic: true}
}

func at(t time.Time) *time.Time { return &t }

func TestValidateFeaturedWindow(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	in := base()
	if a, fields := validateFields(in, now, true); len(fields) > 0 || a.FeaturedUntil != nil || a.CTALabel != "" {
		t.Fatalf("not featured: %+v %v", a, fields)
	}

	in = base()
	in.FeaturedUntil = at(now.Add(48 * time.Hour))
	a, fields := validateFields(in, now, true)
	if len(fields) > 0 || !a.FeaturedFrom.Equal(now) || a.CTALabel != "Continuar" {
		t.Fatalf("from defaults to now and the button to Continuar: %+v %v", a, fields)
	}
	if !a.FeaturedAt(now.Add(time.Hour)) || a.FeaturedAt(now.Add(49*time.Hour)) {
		t.Fatal("featured only inside the window")
	}

	cases := map[string]func(*AnnouncementInput){
		"internal": func(i *AnnouncementInput) { i.IsPublic = false; i.FeaturedUntil = at(now.Add(time.Hour)) },
		"backwards": func(i *AnnouncementInput) {
			i.FeaturedFrom = at(now.Add(2 * time.Hour))
			i.FeaturedUntil = at(now.Add(time.Hour))
		},
		"already over": func(i *AnnouncementInput) {
			i.FeaturedFrom = at(now.Add(-48 * time.Hour))
			i.FeaturedUntil = at(now.Add(-time.Hour))
		},
		"over a year":       func(i *AnnouncementInput) { i.FeaturedUntil = at(now.Add(400 * 24 * time.Hour)) },
		"only from":         func(i *AnnouncementInput) { i.FeaturedFrom = at(now) },
		"bad label":         func(i *AnnouncementInput) { i.FeaturedUntil = at(now.Add(time.Hour)); i.CTALabel = "Clic aquí" },
		"js url":            func(i *AnnouncementInput) { i.FeaturedUntil = at(now.Add(time.Hour)); i.CTAURL = "javascript:alert(1)" },
		"protocol-relative": func(i *AnnouncementInput) { i.FeaturedUntil = at(now.Add(time.Hour)); i.CTAURL = "//evil.example" },
		"http url":          func(i *AnnouncementInput) { i.FeaturedUntil = at(now.Add(time.Hour)); i.CTAURL = "http://example.com" },
	}
	for name, mutate := range cases {
		in := base()
		mutate(&in)
		if _, fields := validateFields(in, now, true); len(fields) == 0 {
			t.Errorf("%s: must be refused", name)
		}
	}

	// An edit that keeps a window that already ended is fine (only a new or changed window must be open)
	in = base()
	in.FeaturedFrom, in.FeaturedUntil = at(now.Add(-48*time.Hour)), at(now.Add(-time.Hour))
	if _, fields := validateFields(in, now, false); len(fields) > 0 {
		t.Fatalf("unchanged past window on edit: %v", fields)
	}
	for _, ok := range []string{"/contact", "/", "https://listercore.es/admision?x=1"} {
		in := base()
		in.FeaturedUntil, in.CTAURL, in.CTALabel = at(now.Add(time.Hour)), ok, "Ir"
		if _, fields := validateFields(in, now, true); len(fields) > 0 {
			t.Errorf("%s: %v", ok, fields)
		}
	}
}

func TestCanEdit(t *testing.T) {
	mine := domain.Announcement{AuthorID: "u1"}
	others := domain.Announcement{AuthorID: "u2"}
	orphan := domain.Announcement{}
	if !canEdit(scope{role: "teacher", userID: "u1"}, mine) || canEdit(scope{role: "teacher", userID: "u1"}, others) {
		t.Fatal("teachers edit only their own")
	}
	if !canEdit(scope{role: "director", userID: "u9"}, others) || !canEdit(scope{role: "secretary", userID: "u9"}, orphan) {
		t.Fatal("managers edit everything")
	}
	if canEdit(scope{role: "student", userID: "u1"}, mine) || canEdit(scope{role: "teacher", userID: ""}, orphan) {
		t.Fatal("students never; nobody owns an orphan by an empty id")
	}
}
