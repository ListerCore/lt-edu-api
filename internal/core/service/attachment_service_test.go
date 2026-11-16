package service

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"api/internal/core/domain"
)

// ooxml builds a minimal zip with the parts that tell Word and Excel apart
func ooxml(t *testing.T, parts ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("<x/>"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var jpegBytes = append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"), make([]byte, 32)...)

func TestSniffType(t *testing.T) {
	cases := map[string]struct {
		data []byte
		want string
	}{
		"pdf":            {pdfBytes, "application/pdf"},
		"png":            {pngBytes, "image/png"},
		"jpeg":           {jpegBytes, "image/jpeg"},
		"webp":           {webpBytes, "image/webp"},
		"docx":           {ooxml(t, "[Content_Types].xml", "word/document.xml"), mimeDOCX},
		"xlsx":           {ooxml(t, "[Content_Types].xml", "xl/workbook.xml"), mimeXLSX},
		"plain zip":      {ooxml(t, "readme.txt"), ""},
		"zip w/o types":  {ooxml(t, "word/document.xml"), ""},
		"executable":     {[]byte("MZ\x90\x00 renamed to .pdf"), ""},
		"html":           {[]byte("<html><script>alert(1)</script></html>"), ""},
		"legacy doc/ole": {append([]byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"), make([]byte, 64)...), ""},
	}
	for name, c := range cases {
		if got := sniffType(c.data); got != c.want {
			t.Errorf("%s: sniffType = %q, want %q", name, got, c.want)
		}
	}
}

func TestValidateUpload(t *testing.T) {
	spec := ownerSpecs[domain.OwnerEnrollment]
	ok := AttachmentUpload{OwnerKind: domain.OwnerEnrollment, OwnerID: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", Category: "dni", Title: "  DNI del estudiante ",
		Files: []UploadFile{{FileName: "../dni.pdf", Data: pdfBytes}, {FileName: "foto.png", Data: pngBytes}}}
	rows, fields := validateUpload(ok, spec)
	if len(fields) > 0 {
		t.Fatalf("valid upload refused: %v", fields)
	}
	if len(rows) != 2 || rows[0].FileName != "dni.pdf" || rows[0].Title != "DNI del estudiante" || rows[1].ContentType != "image/png" || rows[0].Category != "dni" {
		t.Errorf("rows = %+v", rows)
	}

	empty := ok
	empty.Category = ""
	if rows, fields := validateUpload(empty, spec); len(fields) > 0 || rows[0].Category != "other" {
		t.Errorf("empty category must fall back to other: %v", fields)
	}

	tooMany := make([]UploadFile, maxFilesPerUpload+1)
	for i := range tooMany {
		tooMany[i] = UploadFile{FileName: "a.pdf", Data: pdfBytes}
	}
	bad := map[string]struct {
		mutate func(*AttachmentUpload)
		field  string
	}{
		"category of another kind": {func(u *AttachmentUpload) { u.Category = "minutes" }, "category"},
		"unknown category":         {func(u *AttachmentUpload) { u.Category = "selfie" }, "category"},
		"long title":               {func(u *AttachmentUpload) { u.Title = strings.Repeat("a", maxTitleRunes+1) }, "title"},
		"long description":         {func(u *AttachmentUpload) { u.Description = strings.Repeat("a", maxDescRunes+1) }, "description"},
		"no files":                 {func(u *AttachmentUpload) { u.Files = nil }, "files"},
		"too many files":           {func(u *AttachmentUpload) { u.Files = tooMany }, "files"},
		"empty file":               {func(u *AttachmentUpload) { u.Files = []UploadFile{{FileName: "a.pdf"}} }, "files"},
		"renamed executable": {func(u *AttachmentUpload) {
			u.Files = []UploadFile{{FileName: "a.pdf", Data: []byte("MZ\x90\x00 nope")}}
		}, "files"},
		"over 10 MB": {func(u *AttachmentUpload) {
			u.Files = []UploadFile{{FileName: "big.pdf", Data: append(append([]byte{}, pdfBytes...), bytes.Repeat([]byte{0}, 10<<20)...)}}
		}, "files"},
	}
	for name, c := range bad {
		in := ok
		c.mutate(&in)
		if _, fields := validateUpload(in, spec); fields[c.field] == "" {
			t.Errorf("%s: want an error on %q, got %v", name, c.field, fields)
		}
	}
}

func TestEveryOwnerKindHasSpec(t *testing.T) {
	kinds := []string{domain.OwnerEnrollment, domain.OwnerStudent, domain.OwnerStaff, domain.OwnerTutoring, domain.OwnerDiscipline, domain.OwnerPsychology,
		domain.OwnerIndividualPlan, domain.OwnerTherapy, domain.OwnerProgressReport, domain.OwnerInclusionSupport, domain.OwnerPeriod, domain.OwnerClosing,
		domain.OwnerInternship, domain.OwnerDegree, domain.OwnerAttendanceJustification, domain.OwnerApafaMeeting, domain.OwnerAuthorization, domain.OwnerPlatformSheet, domain.OwnerClassroomItem, domain.OwnerLibraryItem}
	if len(ownerSpecs) != len(kinds) {
		t.Fatalf("%d specs for %d kinds", len(ownerSpecs), len(kinds))
	}
	for _, k := range kinds {
		spec, ok := ownerSpecs[k]
		if !ok {
			t.Fatalf("%s has no spec", k)
		}
		if spec.categories[len(spec.categories)-1] != "other" {
			t.Errorf("%s: other must be the last category", k)
		}
		// Whoever writes must also read
		for role := range spec.write {
			if !spec.read[role] {
				t.Errorf("%s: %s writes but cannot read", k, role)
			}
		}
	}
}

func TestOwnerPermissions(t *testing.T) {
	child := "child-1"
	mine := domain.AttachmentOwner{Checked: true, StudentID: child}
	other := domain.AttachmentOwner{Checked: true, StudentID: "child-2"}
	unknown := domain.AttachmentOwner{}
	kids := []string{child}
	cases := []struct {
		name        string
		role, kind  string
		owner       domain.AttachmentOwner
		read, write bool
	}{
		{"superadmin reads psychology", domain.RoleSuperadmin, domain.OwnerPsychology, mine, true, true},
		{"psychologist owns psychology", "psychologist", domain.OwnerPsychology, mine, true, true},
		{"director kept out of psychology", "director", domain.OwnerPsychology, mine, false, false},
		{"teacher kept out of psychology", "teacher", domain.OwnerPsychology, mine, false, false},
		{"secretary manages enrollment", "secretary", domain.OwnerEnrollment, mine, true, true},
		{"coordinator does not see enrollment", "academic_coordinator", domain.OwnerEnrollment, mine, false, false},
		{"teacher reads student file", "teacher", domain.OwnerStudent, mine, true, false},
		{"guardian reads own child", "guardian", domain.OwnerStudent, mine, true, false},
		{"guardian not another child", "guardian", domain.OwnerStudent, other, false, false},
		{"guardian not an unknown student", "guardian", domain.OwnerProgressReport, unknown, false, false},
		{"student not the therapy file", "student", domain.OwnerTherapy, mine, false, false},
		{"guardian attaches to own justification", "guardian", domain.OwnerAttendanceJustification, mine, true, true},
		{"guardian not to another justification", "guardian", domain.OwnerAttendanceJustification, other, false, false},
		{"guardian reads every apafa meeting", "guardian", domain.OwnerApafaMeeting, unknown, true, false},
		{"secretary writes apafa", "secretary", domain.OwnerApafaMeeting, unknown, true, true},
		{"coordinator reads apafa", "academic_coordinator", domain.OwnerApafaMeeting, unknown, true, false},
		{"treasurer not in apafa", "treasurer", domain.OwnerApafaMeeting, unknown, false, false},
		{"coordinator writes period resolutions", "academic_coordinator", domain.OwnerPeriod, unknown, true, true},
		{"secretary only reads periods", "secretary", domain.OwnerPeriod, unknown, true, false},
		{"student not staff legajo", "student", domain.OwnerStaff, unknown, false, false},
		{"specialist writes inclusion support", RoleSpecialEd, domain.OwnerInclusionSupport, unknown, true, true},
	}
	for _, c := range cases {
		spec := ownerSpecs[c.kind]
		if got := canReadOwner(c.role, spec, c.owner, kids); got != c.read {
			t.Errorf("%s: read = %v, want %v", c.name, got, c.read)
		}
		if got := canWriteOwner(c.role, spec, c.owner, kids); got != c.write {
			t.Errorf("%s: write = %v, want %v", c.name, got, c.write)
		}
	}
}

func TestFamiliesDeleteOnlyTheirUploads(t *testing.T) {
	spec := ownerSpecs[domain.OwnerAttendanceJustification]
	owner := domain.AttachmentOwner{Checked: true, StudentID: "child-1"}
	kids := []string{"child-1"}
	theirs := domain.FileAttachment{UploadedBy: "user-guardian"}
	office := domain.FileAttachment{UploadedBy: "user-assistant"}
	if !canDeleteFile("guardian", "user-guardian", spec, owner, kids, theirs) {
		t.Error("guardian must delete what they uploaded")
	}
	if canDeleteFile("guardian", "user-guardian", spec, owner, kids, office) {
		t.Error("guardian must not delete what the school uploaded")
	}
	if !canDeleteFile("assistant", "user-assistant-2", spec, owner, kids, theirs) {
		t.Error("staff who write delete any file of the owner")
	}
	if canDeleteFile("secretary", "user-secretary", spec, owner, kids, office) {
		t.Error("read-only roles delete nothing")
	}
}
