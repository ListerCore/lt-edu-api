package service

import (
	"strings"
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestValidateMeeting(t *testing.T) {
	now := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	good := MeetingInput{Title: "Asamblea de inicio de año", Kind: domain.ApafaAssembly, ScheduledAt: now.Add(48 * time.Hour), Place: " Patio central ", Agenda: "1. Informe\n2. Elección"}
	m, fields := validateMeeting(good, now, true)
	if len(fields) > 0 {
		t.Fatalf("valid meeting rejected: %v", fields)
	}
	if m.Modality != domain.ApafaInPerson || m.Audience != domain.AudienceGuardians || m.Place != "Patio central" {
		t.Errorf("defaults or cleaning wrong: %+v", m)
	}
	cases := []struct {
		name   string
		edit   func(*MeetingInput)
		future bool
		field  string
	}{
		{"short title", func(i *MeetingInput) { i.Title = "Asa" }, true, "title"},
		{"bad kind", func(i *MeetingInput) { i.Kind = "party" }, true, "kind"},
		{"no date", func(i *MeetingInput) { i.ScheduledAt = time.Time{} }, true, "scheduled_at"},
		{"past date for a new meeting", func(i *MeetingInput) { i.ScheduledAt = now.Add(-time.Hour) }, true, "scheduled_at"},
		{"too far", func(i *MeetingInput) { i.ScheduledAt = now.AddDate(3, 0, 0) }, true, "scheduled_at"},
		{"in person without place", func(i *MeetingInput) { i.Place = "  " }, true, "place"},
		{"virtual without link", func(i *MeetingInput) { i.Modality = domain.ApafaVirtual }, true, "meeting_url"},
		{"link not https", func(i *MeetingInput) { i.Modality = domain.ApafaHybrid; i.MeetingURL = "http://meet.example/x" }, true, "meeting_url"},
		{"bad audience", func(i *MeetingInput) { i.Audience = "staff" }, true, "audience"},
		{"agenda too long", func(i *MeetingInput) { i.Agenda = strings.Repeat("a", 4001) }, true, "agenda"},
	}
	for _, c := range cases {
		in := good
		c.edit(&in)
		if _, fields := validateMeeting(in, now, c.future); fields[c.field] == "" {
			t.Errorf("%s: expected an error on %s, got %v", c.name, c.field, fields)
		}
	}
	// Editing a meeting whose date did not change may keep a past date
	past := good
	past.ScheduledAt = now.Add(-time.Hour)
	if _, fields := validateMeeting(past, now, false); len(fields) > 0 {
		t.Errorf("past date kept on edit rejected: %v", fields)
	}
	// A virtual meeting keeps only its link
	virtual := good
	virtual.Modality, virtual.MeetingURL = domain.ApafaVirtual, "https://meet.google.com/abc-defg-hij"
	if m, fields := validateMeeting(virtual, now, true); len(fields) > 0 || m.Place != "" {
		t.Errorf("virtual meeting: %v, place %q", fields, m.Place)
	}
}

func TestValidateMinutes(t *testing.T) {
	n := 42
	if _, fields := validateMinutes(MinutesInput{Minutes: "Se aprobó el plan anual.", Attendees: &n}); len(fields) > 0 {
		t.Errorf("valid minutes rejected: %v", fields)
	}
	neg := -1
	_, fields := validateMinutes(MinutesInput{Minutes: "corto", Attendees: &neg})
	if fields["minutes"] == "" || fields["attendees"] == "" {
		t.Errorf("expected minutes and attendees errors, got %v", fields)
	}
}

func TestValidateBoardMember(t *testing.T) {
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	b, fields := validateBoardMember(BoardMemberInput{FullName: "  María   Quispe ", Position: "president", Phone: "987 654 321", StartsOn: from, EndsOn: from.AddDate(2, 0, -1)})
	if len(fields) > 0 || b.FullName != "María Quispe" {
		t.Fatalf("valid member: %v %q", fields, b.FullName)
	}
	_, fields = validateBoardMember(BoardMemberInput{FullName: "Jo", Position: "king", Phone: "abc", StartsOn: from, EndsOn: from.AddDate(0, 0, -1)})
	for _, f := range []string{"full_name", "position", "phone", "ends_on"} {
		if fields[f] == "" {
			t.Errorf("expected an error on %s, got %v", f, fields)
		}
	}
}

func TestApafaWhenIsLimaTime(t *testing.T) {
	// 23:00 UTC is 18:00 in Lima (UTC-5)
	at := time.Date(2026, 11, 19, 23, 0, 0, 0, time.UTC)
	if got, want := apafaWhen(at), "jueves 19 de noviembre de 2026, 6:00 p. m."; got != want {
		t.Errorf("apafaWhen = %q, want %q", got, want)
	}
	if got := apafaWhen(time.Date(2026, 11, 20, 5, 30, 0, 0, time.UTC)); got != "viernes 20 de noviembre de 2026, 12:30 a. m." {
		t.Errorf("midnight hour: %q", got)
	}
}

func TestApafaNotice(t *testing.T) {
	prev := time.Date(2026, 11, 19, 23, 0, 0, 0, time.UTC)
	n := 120
	m := domain.ApafaMeeting{Title: "Asamblea general", Kind: domain.ApafaAssembly, ScheduledAt: prev.AddDate(0, 0, 7), Place: "Patio", Agenda: "1. Balance",
		Audience: domain.AudienceCommunity, PreviousAt: &prev, RescheduleReason: "Feriado regional", CancelReason: "Paro de transportistas",
		Minutes: "Se aprobó el balance.", Agreements: "Pintar las aulas.", Attendees: &n}
	checks := map[noticeKind][]string{
		noticeConvene:       {"Convocatoria APAFA", "toda la comunidad educativa", "asamblea general", "Lugar: Patio", "Agenda:\n1. Balance"},
		noticeUpdate:        {"Cambios en la reunión APAFA"},
		noticeReschedule:    {"reprogramada", "jueves 19 de noviembre", "Feriado regional"},
		noticeCancel:        {"cancelada", "Paro de transportistas"},
		noticeMinutes:       {"Acta de la reunión APAFA", "Asistentes: 120.", "Pintar las aulas."},
		noticeMinutesUpdate: {"Acta actualizada"},
	}
	for kind, wants := range checks {
		title, body := apafaNotice(m, kind)
		text := title + "\n" + body
		for _, w := range wants {
			if !strings.Contains(text, w) {
				t.Errorf("notice %d lacks %q:\n%s", kind, w, text)
			}
		}
		// The announcement rules (5–160 title, 10–5000 body) must always hold
		if _, fields := validateFields(AnnouncementInput{Kind: domain.KindNotice, Audience: m.Audience, Title: title, Body: body}, time.Now(), false); len(fields) > 0 {
			t.Errorf("notice %d breaks the announcement rules: %v", kind, fields)
		}
	}
	long := m
	long.Title, long.Agenda = strings.Repeat("Título ", 30), strings.Repeat("punto ", 1200)
	title, body := apafaNotice(long, noticeConvene)
	if runeLen(title) > 160 || runeLen(body) > 5000 {
		t.Errorf("notice not clipped: %d / %d", runeLen(title), runeLen(body))
	}
}

func TestApafaRoles(t *testing.T) {
	for _, r := range []string{"director", "secretary", domain.RoleSuperadmin} {
		if !apafaManagers[r] {
			t.Errorf("%s must manage APAFA", r)
		}
	}
	for _, r := range []string{"academic_coordinator", "guardian"} {
		if apafaManagers[r] || !apafaReaders[r] {
			t.Errorf("%s must only read APAFA", r)
		}
	}
	for _, r := range []string{"teacher", "treasurer", "student"} {
		if apafaManagers[r] || apafaReaders[r] {
			t.Errorf("%s must not reach APAFA", r)
		}
	}
	if apafaInstitutionKinds[domain.KindCEPRE] || apafaInstitutionKinds[domain.KindInstitute] {
		t.Error("APAFA is Educación Básica only")
	}
}

func TestDraftMeetingsHiddenFromFamilies(t *testing.T) {
	spec := ownerSpecs[domain.OwnerApafaMeeting]
	draft := domain.AttachmentOwner{Checked: true, FamilyHidden: true}
	if canReadOwner("guardian", spec, draft, nil) {
		t.Error("a guardian opened the files of a meeting not convened")
	}
	if !canReadOwner("secretary", spec, draft, nil) || !canWriteOwner("director", spec, draft, nil) {
		t.Error("staff must keep the files of a draft meeting")
	}
	if !canReadOwner("guardian", spec, domain.AttachmentOwner{Checked: true}, nil) {
		t.Error("a guardian must read the files of a convened meeting")
	}
}
