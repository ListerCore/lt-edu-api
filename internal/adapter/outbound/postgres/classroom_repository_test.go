package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
)

// classroomFixture is a CEBA with one cycle and grade, an active plan with one area, a virtual and an itinerant
// section, a teacher assigned to the area in the virtual one, and a student in each section
type classroomFixture struct {
	repo                                  *ClassroomRepository
	inst, period, grade, subject, teacher string
	virtualStudent, itinerantStudent      string
}

// Runs against a migrated database only: JOSEPH_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func newClassroomFixture(t *testing.T) classroomFixture {
	t.Helper()
	dsn := os.Getenv("JOSEPH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("JOSEPH_TEST_DATABASE_URL not set")
	}
	c, err := New(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ctx := context.Background()
	cleanup := func() { _, _ = c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug = 'repo-test-classroom'`) }
	cleanup()
	t.Cleanup(cleanup)
	f := classroomFixture{repo: NewClassroomRepository(c)}
	q := func(dest *string, sql string, args ...any) {
		t.Helper()
		if err := c.pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var level, curriculum, virtual, itinerant, s1, s2 string
	q(&f.inst, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-classroom', 'Repo Test Classroom', 'eba') RETURNING id`)
	q(&level, `INSERT INTO academic.levels (institution_id, code, name, position, attention_modes) VALUES ($1, 'avanzado', 'Ciclo Avanzado', 0, '{distance}') RETURNING id`, f.inst)
	q(&f.grade, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, '1', '1.° grado', '1.°', 0) RETURNING id`, level)
	q(&f.period, `INSERT INTO academic.periods (institution_id, kind, name, starts_on, ends_on, term_scheme, status)
		VALUES ($1, 'promotional', 'PP 2026', '2026-03-16', '2026-12-11', 'bimester', 'active') RETURNING id`, f.inst)
	q(&curriculum, `INSERT INTO academic.curricula (institution_id, level_id, name, status) VALUES ($1, $2, 'Plan EBA', 'active') RETURNING id`, f.inst, level)
	q(&f.subject, `INSERT INTO academic.subjects (curriculum_id, kind, code, name, position) VALUES ($1, 'area', 'matematica', 'Matemática', 0) RETURNING id`, curriculum)
	q(&f.subject, `WITH x AS (INSERT INTO academic.subject_grades (subject_id, grade_id) VALUES ($1, $2) RETURNING subject_id) SELECT subject_id::text FROM x`, f.subject, f.grade)
	q(&virtual, `INSERT INTO academic.sections (period_id, level_id, grade_id, name, attention_mode, program, distance_variant, capacity)
		VALUES ($1, $2, $3, 'V', 'distance', 'pebaja', 'virtual', 60) RETURNING id`, f.period, level, f.grade)
	q(&itinerant, `INSERT INTO academic.sections (period_id, level_id, grade_id, name, attention_mode, program, distance_variant, capacity)
		VALUES ($1, $2, $3, 'I', 'distance', 'pebaja', 'itinerant', 20) RETURNING id`, f.period, level, f.grade)
	q(&f.teacher, `INSERT INTO people.staff (institution_id, code, document_type, document_number, given_names, surnames, email, position, is_teacher)
		VALUES ($1, 'P-1', 'dni', '40000001', 'Rosa', 'Quispe', 'repo-test-teacher@gmail.com', 'Docente', true) RETURNING id`, f.inst)
	var assignment string
	q(&assignment, `INSERT INTO academic.assignments (institution_id, period_id, section_id, subject_id, staff_id, weekly_hours) VALUES ($1, $2, $3, $4, $5, 4) RETURNING id`,
		f.inst, f.period, virtual, f.subject, f.teacher)
	q(&s1, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname, email)
		VALUES ($1, 'E-1', 'dni', '70000001', 'Ana', 'Ccori', 'repo-test-ana@gmail.com') RETURNING id`, f.inst)
	q(&s2, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname)
		VALUES ($1, 'E-2', 'dni', '70000002', 'Luis', 'Mamani') RETURNING id`, f.inst)
	for _, e := range [][2]string{{s1, virtual}, {s2, itinerant}} {
		var id string
		q(&id, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on)
			VALUES ($1, $2, $3, $4, $5, $6, 'new', '2026-03-16') RETURNING id`, f.inst, e[0], f.period, level, f.grade, e[1])
	}
	f.virtualStudent, f.itinerantStudent = s1, s2
	return f
}

func TestClassroomRoomsSQL(t *testing.T) {
	f := newClassroomFixture(t)
	ctx := context.Background()
	r := f.repo

	if id, err := r.RunningPeriod(ctx, f.inst); err != nil || id != f.period {
		t.Fatalf("running period: %q %v", id, err)
	}
	n, err := r.EnsureRooms(ctx, f.inst, f.period)
	if err != nil || n != 1 {
		t.Fatalf("ensure: %d %v", n, err)
	}
	if again, _ := r.EnsureRooms(ctx, f.inst, f.period); again != 0 {
		t.Fatalf("ensure twice added %d", again)
	}
	rooms, err := r.Rooms(ctx, f.inst, domain.RoomFilter{PeriodID: f.period})
	if err != nil || len(rooms) != 1 {
		t.Fatalf("rooms: %+v %v", rooms, err)
	}
	room := rooms[0]
	if room.StaffID != f.teacher || room.SubjectName != "Matemática" || room.Students != 2 || room.Status != "draft" {
		t.Fatalf("room: %+v", room)
	}
	// The teacher reaches it, a student of the grade only once published, a student of another grade never
	if mine, _ := r.Rooms(ctx, f.inst, domain.RoomFilter{PeriodID: f.period, StaffID: f.teacher}); len(mine) != 1 {
		t.Fatalf("teacher rooms: %d", len(mine))
	}
	if ok, _ := r.TeachesRoom(ctx, f.inst, room.ID, f.teacher); !ok {
		t.Fatal("teacher does not teach the room")
	}
	if pub, _ := r.Rooms(ctx, f.inst, domain.RoomFilter{PeriodID: f.period, GradeIDs: []string{f.grade}, PublishedOnly: true}); len(pub) != 0 {
		t.Fatal("draft room listed for students")
	}
	if none, _ := r.Rooms(ctx, f.inst, domain.RoomFilter{PeriodID: f.period, GradeIDs: []string{}}); len(none) != 0 {
		t.Fatal("empty grade filter matched rooms")
	}
	room.Status, room.Description = "published", "Aula de matemática"
	if room, err = r.UpdateRoom(ctx, f.inst, room); err != nil || room.Status != "published" {
		t.Fatalf("update: %+v %v", room, err)
	}

	grades, err := r.StudentGrades(ctx, f.inst, f.period, "REPO-TEST-ANA@gmail.com")
	if err != nil || grades[f.virtualStudent] != f.grade || len(grades) != 1 {
		t.Fatalf("student grades: %v %v", grades, err)
	}
	if st, err := r.StaffByEmail(ctx, f.inst, "repo-test-teacher@gmail.com"); err != nil || st.ID != f.teacher {
		t.Fatalf("staff by email: %+v %v", st, err)
	}

	// Units and items in order, with the library feeding an item
	u1, err := r.SaveUnit(ctx, f.inst, domain.Unit{RoomID: room.ID, Title: "Semana 1", Published: true})
	if err != nil || u1.Position != 0 {
		t.Fatalf("unit 1: %+v %v", u1, err)
	}
	u2, _ := r.SaveUnit(ctx, f.inst, domain.Unit{RoomID: room.ID, Title: "Semana 2"})
	lib, err := r.SaveLibraryItem(ctx, f.inst, "", "Docente", domain.LibraryItem{Kind: "video", Title: "Fracciones", URL: "https://example.com/v"})
	if err != nil {
		t.Fatal(err)
	}
	reading, err := r.SaveItem(ctx, f.inst, "", domain.Item{UnitID: u1.ID, Kind: "reading", Title: "Lectura", Body: "Texto", Published: true})
	if err != nil || reading.RoomID != room.ID {
		t.Fatalf("reading: %+v %v", reading, err)
	}
	video, err := r.SaveItem(ctx, f.inst, "", domain.Item{UnitID: u1.ID, Kind: "video", Title: "Fracciones", LibraryItemID: lib.ID, Published: true})
	if err != nil || video.URL != "https://example.com/v" || video.Position != 1 {
		t.Fatalf("library video: %+v %v", video, err)
	}
	if err := r.ReorderItems(ctx, f.inst, u1.ID, []string{video.ID, reading.ID}); err != nil {
		t.Fatal(err)
	}
	if err := r.ReorderItems(ctx, f.inst, u1.ID, []string{video.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("partial reorder: %v", err)
	}
	if err := r.ReorderUnits(ctx, f.inst, room.ID, []string{u2.ID, u1.ID}); err != nil {
		t.Fatal(err)
	}

	// A view by the student is marked for them and counted for staff
	if err := r.RecordView(ctx, reading.ID, f.virtualStudent, time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = r.RecordView(ctx, reading.ID, f.virtualStudent, time.Now())
	units, err := r.Units(ctx, room.ID, true, f.virtualStudent)
	if err != nil || len(units) != 2 || units[0].ID != u2.ID || len(units[1].Items) != 2 || units[1].Items[0].ID != video.ID {
		t.Fatalf("units: %+v %v", units, err)
	}
	if read := units[1].Items[1]; !read.Viewed || read.Views != 1 {
		t.Fatalf("reading views: %+v", read)
	}
	if lib, _ = r.LibraryItem(ctx, f.inst, lib.ID); lib.Uses != 1 {
		t.Fatalf("library uses: %d", lib.Uses)
	}

	// Itinerant deliveries: only the student of the itinerant section is listed and can be recorded
	deliveries, err := r.Deliveries(ctx, f.inst, u1.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].StudentID != f.itinerantStudent || deliveries[0].DeliveredOn != nil {
		t.Fatalf("deliveries: %+v %v", deliveries, err)
	}
	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if err := r.SaveDelivery(ctx, f.inst, domain.Delivery{UnitID: u1.ID, StudentID: f.itinerantStudent, DeliveredOn: &day, RecordedByName: "Tutor"}); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveDelivery(ctx, f.inst, domain.Delivery{UnitID: u1.ID, StudentID: f.virtualStudent, DeliveredOn: &day}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("virtual student delivery: %v", err)
	}
	deliveries, _ = r.Deliveries(ctx, f.inst, u1.ID)
	if deliveries[0].DeliveredOn == nil || deliveries[0].RecordedByName != "Tutor" {
		t.Fatalf("recorded delivery: %+v", deliveries[0])
	}

	// Copying into a room that already has units is refused
	if _, err := r.CopyUnits(ctx, f.inst, room.ID, room.ID); !errors.Is(err, domain.ErrRoomHasMaterial) && !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("copy into itself: %v", err)
	}
	if err := r.DeleteItem(ctx, f.inst, video.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteLibraryItem(ctx, f.inst, lib.ID); err != nil {
		t.Fatal(err)
	}
}

func TestClassroomAssessmentSQL(t *testing.T) {
	f := newClassroomFixture(t)
	ctx := context.Background()
	r := f.repo
	if _, err := r.EnsureRooms(ctx, f.inst, f.period); err != nil {
		t.Fatal(err)
	}
	rooms, _ := r.Rooms(ctx, f.inst, domain.RoomFilter{PeriodID: f.period})
	room := rooms[0]
	var comp, term string
	if err := r.c.pool.QueryRow(ctx, `INSERT INTO academic.competencies (curriculum_id, subject_id, kind, name, position)
		SELECT curriculum_id, id, 'competency', 'Resuelve problemas de cantidad', 0 FROM academic.subjects WHERE id = $1 RETURNING id`, f.subject).Scan(&comp); err != nil {
		t.Fatal(err)
	}
	if err := r.c.pool.QueryRow(ctx, `INSERT INTO academic.terms (period_id, number, name, starts_on, ends_on) VALUES ($1, 4, 'IV bimestre', '2026-10-19', '2026-12-11') RETURNING id`, f.period).Scan(&term); err != nil {
		t.Fatal(err)
	}
	unit, _ := r.SaveUnit(ctx, f.inst, domain.Unit{RoomID: room.ID, Title: "Unidad 1", Published: true})
	quiz, err := r.SaveItem(ctx, f.inst, "", domain.Item{UnitID: unit.ID, Kind: "quiz", Title: "Cuestionario", Published: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureAssessment(ctx, quiz.ID); err != nil {
		t.Fatal(err)
	}
	if comps, _ := r.RoomCompetencies(ctx, f.inst, room.ID); len(comps) != 1 || comps[0].ID != comp {
		t.Fatalf("competencies: %+v", comps)
	}
	a, err := r.SaveAssessment(ctx, f.inst, domain.Assessment{ItemID: quiz.ID, MaxAttempts: 2, CompetencyIDs: []string{comp}, ShowAnswers: true},
		[]domain.QuizQuestion{{Kind: "single", Prompt: "¿1/2 de 10?", Options: []string{"5", "2"}, Correct: []int{0}, Points: 2}, {Kind: "open", Prompt: "Explica", Points: 3}})
	if err != nil || len(a.Questions) != 2 || a.Questions[0].Correct[0] != 0 || a.MaxAttempts != 2 {
		t.Fatalf("assessment: %+v %v", a, err)
	}
	// A competency of another area is refused
	if _, err := r.SaveAssessment(ctx, f.inst, domain.Assessment{ItemID: quiz.ID, MaxAttempts: 1, CompetencyIDs: []string{f.subject}}, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign competency: %v", err)
	}

	sub, err := r.CreateAttempt(ctx, quiz.ID, f.virtualStudent, 1)
	if err != nil || sub.Status != "in_progress" || sub.StudentName == "" {
		t.Fatalf("attempt: %+v %v", sub, err)
	}
	answers := map[string]domain.QuizAnswer{a.Questions[0].ID: {Choices: []int{0}}, a.Questions[1].ID: {Text: "La mitad"}}
	if err := r.SaveAnswers(ctx, sub.ID, answers); err != nil {
		t.Fatal(err)
	}
	if err := r.HandIn(ctx, sub.ID, 2, 2, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveAnswers(ctx, sub.ID, answers); !errors.Is(err, domain.ErrAttemptFinished) {
		t.Fatalf("answers after hand-in: %v", err)
	}
	if n, _ := r.HandedIn(ctx, quiz.ID); n != 1 {
		t.Fatalf("handed in: %d", n)
	}
	sub.Levels, sub.OpenPoints, sub.Feedback = map[string]string{comp: "A"}, map[string]int{a.Questions[1].ID: 2}, "Bien resuelto"
	graded, err := r.Grade(ctx, f.inst, sub, "", "Docente")
	if err != nil || graded.Status != "graded" || graded.Levels[comp] != "A" || graded.OpenPoints[a.Questions[1].ID] != 2 || graded.Answers[a.Questions[0].ID].Choices[0] != 0 {
		t.Fatalf("graded: %+v %v", graded, err)
	}

	roster, err := r.Roster(ctx, f.inst, quiz.ID)
	if err != nil || len(roster) != 2 {
		t.Fatalf("roster: %+v %v", roster, err)
	}
	for _, row := range roster {
		if row.StudentID == f.virtualStudent && (row.Latest == nil || row.Attempts != 1) {
			t.Fatalf("roster row: %+v", row)
		}
	}
	until := time.Now().Add(72 * time.Hour)
	if err := r.SetExtension(ctx, quiz.ID, f.itinerantStudent, &until, "Trabaja de noche", "Docente"); err != nil {
		t.Fatal(err)
	}
	if ext, _ := r.Extension(ctx, quiz.ID, f.itinerantStudent); ext == nil {
		t.Fatal("extension not saved")
	}

	// The virtual student's section has no teaching assignment yet: the target says so
	targets, err := r.RecordTargets(ctx, f.inst, quiz.ID)
	if err != nil || len(targets) != 1 || targets[0].Levels[comp] != "A" || targets[0].SectionID == "" {
		t.Fatalf("targets: %+v %v", targets, err)
	}
	day := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if id, _ := r.OpenTerm(ctx, f.period, day); id != term {
		t.Fatalf("open term: %q", id)
	}
	if id, _ := r.OpenTerm(ctx, f.period, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); id != "" {
		t.Fatalf("before any term: %q", id)
	}
	if err := r.MarkRecorded(ctx, []string{sub.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestClassroomForumSQL(t *testing.T) {
	f := newClassroomFixture(t)
	ctx := context.Background()
	r := f.repo
	if _, err := r.EnsureRooms(ctx, f.inst, f.period); err != nil {
		t.Fatal(err)
	}
	rooms, _ := r.Rooms(ctx, f.inst, domain.RoomFilter{PeriodID: f.period})
	unit, _ := r.SaveUnit(ctx, f.inst, domain.Unit{RoomID: rooms[0].ID, Title: "Unidad 1", Published: true})
	item, err := r.SaveItem(ctx, f.inst, "", domain.Item{UnitID: unit.ID, Kind: "forum", Title: "¿Dónde usas fracciones?", Body: "Cuéntanos", Published: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureForum(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	forum, err := r.Forum(ctx, f.inst, item.ID)
	if err != nil || forum.Kind != "debate" || forum.Closed || forum.Prompt != "Cuéntanos" {
		t.Fatalf("forum: %+v %v", forum, err)
	}
	thread, err := r.CreatePost(ctx, domain.ForumPost{ItemID: item.ID, AuthorStudentID: f.virtualStudent, AuthorName: "Ana", AuthorRole: "student", Body: "En el mercado"})
	if err != nil || thread.ParentID != "" {
		t.Fatalf("thread: %+v %v", thread, err)
	}
	reply, err := r.CreatePost(ctx, domain.ForumPost{ItemID: item.ID, ParentID: thread.ID, AuthorName: "Docente", AuthorRole: "teacher", Body: "¡Bien!"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreatePost(ctx, domain.ForumPost{ItemID: item.ID, ParentID: reply.ID, AuthorName: "Ana", Body: "Gracias"}); !errors.Is(err, domain.ErrReplyToReply) {
		t.Fatalf("reply to a reply: %v", err)
	}
	if err := r.DeletePost(ctx, thread.ID); !errors.Is(err, domain.ErrPostHasReplies) {
		t.Fatalf("delete with replies: %v", err)
	}
	thread.Pinned = true
	if err := r.ModeratePost(ctx, thread); err != nil {
		t.Fatal(err)
	}
	reply.Hidden, reply.HiddenReason, reply.HiddenByName = true, "Prueba", "Coordinación"
	if err := r.ModeratePost(ctx, reply); err != nil {
		t.Fatal(err)
	}
	if err := r.EditPost(ctx, reply.ID, "x", time.Now()); !errors.Is(err, domain.ErrPostHidden) {
		t.Fatalf("edit hidden: %v", err)
	}
	posts, err := r.Posts(ctx, item.ID)
	if err != nil || len(posts) != 2 || !posts[0].Pinned || posts[0].Replies != 1 || !posts[1].Hidden {
		t.Fatalf("posts: %+v %v", posts, err)
	}
	part, err := r.ForumParticipation(ctx, f.inst, item.ID)
	if err != nil || len(part) != 2 {
		t.Fatalf("participation: %+v %v", part, err)
	}
	for _, p := range part {
		if p.StudentID == f.virtualStudent && (p.Threads != 1 || p.LastAt == nil) {
			t.Fatalf("student participation: %+v", p)
		}
	}
	forum.Closed, forum.ClosedByName = true, "Docente"
	if err := r.SaveForum(ctx, f.inst, forum); err != nil {
		t.Fatal(err)
	}
	if fo, _ := r.Forum(ctx, f.inst, item.ID); !fo.Closed {
		t.Fatal("forum not closed")
	}
}
