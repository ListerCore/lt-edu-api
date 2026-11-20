package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestTutoringSQL(t *testing.T) {
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
		_, _ = c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug IN ('repo-test-tutoring', 'repo-test-tutoring-2')`)
	}
	cleanup()
	t.Cleanup(cleanup)
	q := func(dest *string, sql string, args ...any) {
		t.Helper()
		if err := c.pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var inst, other, level, grade, period, section, tutorA, tutorB, ana, luis, eva string
	q(&inst, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-tutoring', 'Repo Test Tutoría', 'eba') RETURNING id`)
	q(&other, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-tutoring-2', 'Otro', 'eba') RETURNING id`)
	q(&level, `INSERT INTO academic.levels (institution_id, code, name, position, attention_modes) VALUES ($1, 'avanzado', 'Ciclo Avanzado', 0, '{distance}') RETURNING id`, inst)
	q(&grade, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, '3', '3.° grado', '3.°', 0) RETURNING id`, level)
	q(&period, `INSERT INTO academic.periods (institution_id, kind, name, starts_on, ends_on, term_scheme, status)
		VALUES ($1, 'promotional', 'PP 2026', '2026-03-16', '2026-12-11', 'bimester', 'active') RETURNING id`, inst)
	q(&tutorA, `INSERT INTO people.staff (institution_id, code, document_type, document_number, given_names, surnames, position, is_teacher)
		VALUES ($1, 'D-1', 'dni', '40000001', 'Rosa', 'Quispe', 'Tutora virtual', true) RETURNING id`, inst)
	q(&tutorB, `INSERT INTO people.staff (institution_id, code, document_type, document_number, given_names, surnames, position, is_teacher)
		VALUES ($1, 'D-2', 'dni', '40000002', 'Juan', 'Mamani', 'Docente', true) RETURNING id`, inst)
	q(&section, `INSERT INTO academic.sections (period_id, level_id, grade_id, name, attention_mode, distance_variant, program, capacity, tutor_id)
		VALUES ($1, $2, $3, 'V', 'distance', 'virtual', 'pebaja', 30, $4) RETURNING id`, period, level, grade, tutorA)
	q(&ana, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname) VALUES ($1, 'E-1', 'dni', '70000031', 'Ana', 'Ccori') RETURNING id`, inst)
	q(&luis, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname) VALUES ($1, 'E-2', 'dni', '70000032', 'Luis', 'Huamán') RETURNING id`, inst)
	q(&eva, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname) VALUES ($1, 'E-3', 'dni', '70000033', 'Eva', 'Torres') RETURNING id`, inst)
	var x string
	for _, st := range []string{ana, luis, eva} {
		q(&x, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on)
			VALUES ($1, $2, $3, $4, $5, $6, 'new', '2026-03-16') RETURNING id`, inst, st, period, level, grade, section)
	}
	r := NewTutoringRepository(c)

	// Tutor in force: the section's, then Eva gets one of her own
	if p, err := r.RunningPeriod(ctx, inst); err != nil || p != period {
		t.Fatalf("period: %s %v", p, err)
	}
	if n, err := r.AssignTutor(ctx, other, period, []string{eva}, tutorB, "x"); err != nil || n != 0 {
		t.Fatalf("foreign assign: %d %v", n, err)
	}
	if n, err := r.AssignTutor(ctx, inst, period, []string{eva}, tutorB, "Directora"); err != nil || n != 1 {
		t.Fatalf("assign: %d %v", n, err)
	}
	mine, err := r.TutorStudents(ctx, inst, period, tutorA)
	if err != nil || len(mine) != 2 || mine[0].Individual || mine[0].StaffName != "Rosa Quispe" || mine[0].Placement != "3.° V · Ciclo Avanzado" {
		t.Fatalf("tutor A: %+v %v", mine, err)
	}
	if b, _ := r.TutorStudents(ctx, inst, period, tutorB); len(b) != 1 || !b[0].Individual || b[0].StudentID != eva {
		t.Fatalf("tutor B: %+v", b)
	}
	loads, err := r.TutorLoads(ctx, inst, period)
	if err != nil || len(loads) != 2 || loads[0].StaffID != tutorA || loads[0].BySection != 2 || len(loads[0].Sections) != 1 || loads[1].Individual != 1 {
		t.Fatalf("loads: %+v %v", loads, err)
	}
	if ids, _ := r.IndividualTutees(ctx, inst, tutorB); len(ids) != 1 || ids[0] != eva {
		t.Fatalf("tutees: %v", ids)
	}
	if cand, _ := r.TutorCandidates(ctx, inst); len(cand) != 2 {
		t.Fatalf("candidates: %+v", cand)
	}

	// Sessions: schedule with two students, change it, hold it with one: one interview, activity recorded
	at := time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)
	s, err := r.SaveSession(ctx, inst, domain.TutoringSession{StaffID: tutorA, Kind: domain.SessionGroup, Mode: "virtual", ScheduledAt: at, DurationMin: 45,
		Link: "https://meet.example/abc", Reason: "Retomar el ritmo", AlertKind: "inactive", CreatedName: "Rosa"}, []string{ana, luis})
	if err != nil || len(s.Attendees) != 2 || s.Status != domain.SessionScheduled || s.StaffName != "Rosa Quispe" {
		t.Fatalf("session: %+v %v", s, err)
	}
	if _, err := r.SaveSession(ctx, other, domain.TutoringSession{StaffID: tutorA, Kind: "group", Mode: "virtual", ScheduledAt: at, DurationMin: 30, Reason: "x"}, nil); err == nil {
		t.Fatal("staff of another institution")
	}
	s.DurationMin = 30
	if s, err = r.SaveSession(ctx, inst, s, []string{ana, luis}); err != nil || s.DurationMin != 30 {
		t.Fatalf("update: %+v %v", s, err)
	}
	if list, _ := r.Sessions(ctx, domain.SessionFilter{InstitutionID: inst, StudentID: luis}); len(list) != 1 {
		t.Fatalf("by student: %d", len(list))
	}
	held, err := r.HoldSession(ctx, inst, port.HoldInput{SessionID: s.ID, Attended: []string{ana}, Notes: "Trabaja de noche", Agreements: "Conectarse los sábados",
		HeldOn: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), Topic: "Retomar el ritmo", AuthorName: "Rosa", At: at.Add(time.Hour)})
	if err != nil || held.Status != domain.SessionHeld || held.HeldAt == nil {
		t.Fatalf("hold: %+v %v", held, err)
	}
	for _, a := range held.Attendees {
		came := a.StudentID == ana
		if a.Attended == nil || *a.Attended != came || (a.InterviewID != "") != came {
			t.Fatalf("attendee: %+v", a)
		}
	}
	var interviews, tutoring string
	q(&interviews, `SELECT count(*)::text FROM wellbeing.interviews WHERE student_id = $1 AND agreements = 'Conectarse los sábados'`, ana)
	q(&tutoring, `SELECT count(*)::text FROM attendance.activity_events WHERE student_id = $1 AND kind = 'tutoring'`, ana)
	if interviews != "1" || tutoring != "1" {
		t.Fatalf("interview %s, activity %s", interviews, tutoring)
	}
	if _, err := r.HoldSession(ctx, inst, port.HoldInput{SessionID: s.ID, At: at}); err != domain.ErrSessionClosed {
		t.Fatalf("hold twice: %v", err)
	}
	if err := r.SetSessionStatus(ctx, inst, s.ID, domain.SessionCancelled); err != domain.ErrSessionClosed {
		t.Fatalf("cancel held: %v", err)
	}

	// Messages: thread once per pair, unread from each side, a student's message is activity
	th, err := r.EnsureThread(ctx, inst, ana, tutorA)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := r.EnsureThread(ctx, inst, ana, tutorA); again.ID != th.ID {
		t.Fatal("thread twice")
	}
	if _, err := r.EnsureThread(ctx, other, ana, tutorA); err == nil {
		t.Fatal("foreign thread")
	}
	if _, err := r.AddMessage(ctx, th.ID, domain.Message{Side: domain.SideStaff, AuthorName: "Rosa", Body: "Hola Ana, ¿cómo vas?"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddMessage(ctx, th.ID, domain.Message{Side: domain.SideStudent, AuthorName: "Ana", Body: "Bien, profesora"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddMessage(ctx, th.ID, domain.Message{Side: domain.SideStudent, AuthorName: "Ana", Body: "Mañana entrego"}, ""); err != nil {
		t.Fatal(err)
	}
	staffSide, err := r.Threads(ctx, inst, tutorA, "")
	if err != nil || len(staffSide) != 1 || staffSide[0].Unread != 2 || staffSide[0].LastBody != "Mañana entrego" || staffSide[0].LastSide != domain.SideStudent {
		t.Fatalf("staff threads: %+v %v", staffSide, err)
	}
	if st, _ := r.Threads(ctx, inst, "", ana); len(st) != 1 || st[0].Unread != 0 {
		t.Fatalf("student threads: %+v", st)
	}
	if err := r.MarkRead(ctx, th.ID, domain.SideStaff, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.Threads(ctx, inst, tutorA, ""); st[0].Unread != 0 {
		t.Fatalf("read: %+v", st)
	}
	if msgs, _ := r.Messages(ctx, th.ID, 2); len(msgs) != 2 || msgs[1].Body != "Mañana entrego" {
		t.Fatalf("last page: %+v", msgs)
	}
	q(&x, `SELECT count(*)::text FROM attendance.activity_events WHERE student_id = $1 AND kind = 'post'`, ana)
	if x != "2" {
		t.Fatalf("message activity: %s", x)
	}
	if _, err := r.Thread(ctx, other, th.ID); err != domain.ErrNotFound {
		t.Fatalf("foreign thread read: %v", err)
	}

	// SAANEE
	ref, err := r.CreateSaanee(ctx, inst, domain.SaaneeReferral{StudentID: luis, Reason: "Dificultad para leer", SentOn: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), ReferredName: "Rosa"})
	if err != nil || ref.Status != domain.SaaneeSent || ref.StudentName != "Huamán, Luis" {
		t.Fatalf("saanee: %+v %v", ref, err)
	}
	ref.Status, ref.Response, ref.UpdatedName = domain.SaaneeAttending, "Evaluación el lunes", "Directora"
	if ref, err = r.UpdateSaanee(ctx, inst, ref); err != nil || ref.Status != domain.SaaneeAttending {
		t.Fatalf("update saanee: %+v %v", ref, err)
	}
	if _, err := r.UpdateSaanee(ctx, other, ref); err != domain.ErrNotFound {
		t.Fatalf("foreign saanee: %v", err)
	}
	if list, _ := r.SaaneeReferrals(ctx, inst, []string{ana}); len(list) != 0 {
		t.Fatalf("filtered: %+v", list)
	}
}
