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
