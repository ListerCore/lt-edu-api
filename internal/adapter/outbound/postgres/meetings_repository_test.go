package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

var _ port.MeetingsRepository = (*MeetingsRepository)(nil)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestMeetingsSQL(t *testing.T) {
	dsn := os.Getenv("LTEDU_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LTEDU_TEST_DATABASE_URL not set")
	}
	c, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()
	cleanup := func() {
		_, _ = c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug IN ('repo-test-meetings', 'repo-test-meetings-2')`)
		_, _ = c.pool.Exec(ctx, `DELETE FROM iam.users WHERE email IN ('meet.host@repo.test', 'meet.ana@repo.test')`)
	}
	cleanup()
	t.Cleanup(cleanup)
	q := func(dest *string, sql string, args ...any) {
		t.Helper()
		if err := c.pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var inst, other, level, grade, subject, period, room, unit, ana, host, anaUser, kase string
	q(&inst, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-meetings', 'Repo Test Meetings', 'eba') RETURNING id`)
	q(&other, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-meetings-2', 'Otro', 'eba') RETURNING id`)
	q(&level, `INSERT INTO academic.levels (institution_id, code, name, position, attention_modes) VALUES ($1, 'avanzado', 'Ciclo Avanzado', 0, '{distance}') RETURNING id`, inst)
	q(&grade, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, '3', '3.° grado', '3.°', 0) RETURNING id`, level)
	var curriculum string
	q(&curriculum, `INSERT INTO academic.curricula (institution_id, level_id, name, status) VALUES ($1, $2, 'Plan EBA', 'active') RETURNING id`, inst, level)
	q(&subject, `INSERT INTO academic.subjects (curriculum_id, kind, code, name, position) VALUES ($1, 'area', 'matematica', 'Matemática', 0) RETURNING id`, curriculum)
	q(&period, `INSERT INTO academic.periods (institution_id, kind, name, starts_on, ends_on, term_scheme, status)
		VALUES ($1, 'promotional', 'PP 2026', '2026-03-16', '2026-12-11', 'bimester', 'active') RETURNING id`, inst)
	q(&room, `INSERT INTO classroom.rooms (institution_id, period_id, subject_id, grade_id, status) VALUES ($1, $2, $3, $4, 'published') RETURNING id`, inst, period, subject, grade)
	q(&unit, `INSERT INTO classroom.units (room_id, position, title, published) VALUES ($1, 0, 'Unidad 1', true) RETURNING id`, room)
	q(&ana, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname) VALUES ($1, 'E-1', 'dni', '70000041', 'Ana', 'Ccori') RETURNING id`, inst)
	q(&host, `INSERT INTO iam.users (email, full_name) VALUES ('meet.host@repo.test', 'Rosa Quispe') RETURNING id`)
	q(&anaUser, `INSERT INTO iam.users (email, full_name) VALUES ('meet.ana@repo.test', 'Ana Ccori') RETURNING id`)
	q(&kase, `INSERT INTO wellbeing.psych_cases (institution_id, student_id, origin, reason, opened_on) VALUES ($1, $2, 'self', 'Estrés', '2026-11-20') RETURNING id`, inst, ana)

	start := time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)
	var platform, meet, draft string
	q(&platform, `INSERT INTO classroom.items (room_id, unit_id, position, kind, title, starts_at, duration_minutes, published, provider)
		VALUES ($1, $2, 0, 'live', 'Clase 1', $3, 60, true, 'platform') RETURNING id`, room, unit, start)
	q(&meet, `INSERT INTO classroom.items (room_id, unit_id, position, kind, title, url, starts_at, duration_minutes, published)
		VALUES ($1, $2, 1, 'live', 'Clase 2', 'https://meet.google.com/abc', $3, 45, true) RETURNING id`, room, unit, start.Add(24*time.Hour))
	q(&draft, `INSERT INTO classroom.items (room_id, unit_id, position, kind, title, starts_at, duration_minutes, published, provider)
		VALUES ($1, $2, 2, 'live', 'Borrador', $3, 30, false, 'platform') RETURNING id`, room, unit, start.Add(2*time.Hour))
	r := NewMeetingsRepository(c)

	// Live classes: staff see drafts too, students only what is published; the range and the institution filter
	from, to := start.Add(-time.Hour), start.Add(48*time.Hour)
	all, err := r.LiveItems(ctx, inst, []string{room}, from, to, false, start)
	if err != nil || len(all) != 3 || all[0].ID != platform || all[0].Provider != "platform" || all[0].DurationMin != 60 || all[0].RoomID != room {
		t.Fatalf("staff live items = %+v, %v", all, err)
	}
	if all[2].ID != meet || all[2].Provider != "meet" || all[2].Link != "https://meet.google.com/abc" {
		t.Fatalf("meet item = %+v", all[2])
	}
	pub, err := r.LiveItems(ctx, inst, []string{room}, from, to, true, start)
	if err != nil || len(pub) != 2 {
		t.Fatalf("student live items = %+v, %v", pub, err)
	}
	if got, _ := r.LiveItems(ctx, other, []string{room}, from, to, false, start); len(got) != 0 {
		t.Fatalf("another institution sees %+v", got)
	}

	// Joins: the first entry stays, the count grows, the student is kept
	if err := r.RecordJoin(ctx, inst, domain.SourceClass, platform, anaUser, ana, "Ana Ccori", start); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordJoin(ctx, inst, domain.SourceClass, platform, anaUser, "", "Ana Ccori", start.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordJoin(ctx, inst, domain.SourceClass, platform, host, "", "Rosa Quispe", start.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	joins, err := r.Joins(ctx, inst, domain.SourceClass, platform)
	if err != nil || len(joins) != 2 || joins[0].UserID != host || joins[1].Joins != 2 || joins[1].StudentID != ana || !joins[1].FirstAt.Equal(start) || !joins[1].LastAt.Equal(start.Add(10*time.Minute)) {
		t.Fatalf("joins = %+v, %v", joins, err)
	}
	if got, _ := r.Joins(ctx, other, domain.SourceClass, platform); len(got) != 0 {
		t.Fatalf("another institution reads joins %+v", got)
	}

	// Appointments: created from the case (its student), rescheduled back to scheduled, status, filters
	a, err := r.SaveAppointment(ctx, inst, domain.PsychAppointment{CaseID: kase, ScheduledAt: start, DurationMin: 45, Mode: "virtual", Provider: "platform", HostUserID: host, CreatedName: "Rosa Quispe"})
	if err != nil || a.StudentID != ana || a.StudentName != "Ana Ccori" || a.Status != "scheduled" || a.HostUserID != host {
		t.Fatalf("appointment = %+v, %v", a, err)
	}
	if _, err := r.SaveAppointment(ctx, other, domain.PsychAppointment{CaseID: kase, ScheduledAt: start, DurationMin: 45, Mode: "virtual", Provider: "platform"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another institution's case: %v", err)
	}
	if a, err = r.SetAppointmentStatus(ctx, inst, a.ID, "cancelled"); err != nil || a.Status != "cancelled" {
		t.Fatalf("cancel = %+v, %v", a, err)
	}
	a.ScheduledAt, a.Provider, a.Link = start.Add(time.Hour), "meet", "https://meet.google.com/x"
	if a, err = r.SaveAppointment(ctx, inst, a); err != nil || a.Status != "scheduled" || a.Provider != "meet" || !a.ScheduledAt.Equal(start.Add(time.Hour)) {
		t.Fatalf("reschedule = %+v, %v", a, err)
	}
	if list, err := r.Appointments(ctx, inst, kase, "", nil, nil); err != nil || len(list) != 1 {
		t.Fatalf("by case = %+v, %v", list, err)
	}
	late := start.Add(2 * time.Hour)
	if list, err := r.Appointments(ctx, inst, "", ana, &late, nil); err != nil || len(list) != 0 {
		t.Fatalf("after the appointment = %+v, %v", list, err)
	}
	if _, err := r.SetAppointmentStatus(ctx, other, a.ID, "held"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("another institution's appointment: %v", err)
	}
}
