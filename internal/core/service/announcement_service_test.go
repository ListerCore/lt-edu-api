package service

import (
	"bytes"
	"strings"
	"testing"

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
	got, problem := classifyFiles(files)
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
		if _, problem := classifyFiles(files); problem == "" {
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
