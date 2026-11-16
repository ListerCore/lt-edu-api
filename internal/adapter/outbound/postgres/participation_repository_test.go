package postgres

import (
	"context"
	"testing"
	"time"

	"api/internal/core/domain"
)

func TestParticipationSQL(t *testing.T) {
	f := newClassroomFixture(t)
	ctx := context.Background()
	cr := f.repo
	pr := NewParticipationRepository(cr.c)
	if _, err := cr.EnsureRooms(ctx, f.inst, f.period); err != nil {
		t.Fatal(err)
	}
	rooms, _ := cr.Rooms(ctx, f.inst, domain.RoomFilter{PeriodID: f.period})
	room := rooms[0]
	room.Status = domain.RoomPublished
	if _, err := cr.UpdateRoom(ctx, f.inst, room); err != nil {
		t.Fatal(err)
	}
	unit, _ := cr.SaveUnit(ctx, f.inst, domain.Unit{RoomID: room.ID, Title: "Unidad 1", Published: true})
	reading, _ := cr.SaveItem(ctx, f.inst, "", domain.Item{UnitID: unit.ID, Kind: "reading", Title: "Lectura", Body: "x", Published: true})
	task, _ := cr.SaveItem(ctx, f.inst, "", domain.Item{UnitID: unit.ID, Kind: "task", Title: "Tarea", Published: true})
	if err := cr.EnsureAssessment(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if _, err := cr.c.pool.Exec(ctx, `UPDATE classroom.assessments SET closes_at = $2 WHERE item_id = $1`, task.ID, past); err != nil {
		t.Fatal(err)
	}

	// Each action becomes an event through the triggers
	at := time.Now().Add(-time.Hour)
	if err := cr.RecordView(ctx, reading.ID, f.virtualStudent, at); err != nil {
		t.Fatal(err)
	}
	if err := cr.RecordView(ctx, reading.ID, f.virtualStudent, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := cr.CreatePost(ctx, domain.ForumPost{ItemID: mustForum(t, cr, f.inst, unit.ID), AuthorStudentID: f.virtualStudent, AuthorName: "Ana", Body: "Hola"}); err != nil {
		t.Fatal(err)
	}
	var events int
	_ = cr.c.pool.QueryRow(ctx, `SELECT count(*) FROM attendance.activity_events WHERE student_id = $1`, f.virtualStudent).Scan(&events)
	if events != 3 {
		t.Fatalf("events: %d", events)
	}
	if _, err := cr.c.pool.Exec(ctx, `UPDATE attendance.activity_events SET kind = 'login' WHERE student_id = $1`, f.virtualStudent); err == nil {
		t.Fatal("events must be append-only")
	}

	today := time.Now()
	act, err := pr.Activity(ctx, []string{f.virtualStudent, f.itinerantStudent}, today.AddDate(0, 0, -7), today)
	if err != nil || len(act) != 1 || act[0].Views != 2 || act[0].Posts != 1 || len(act[0].Days) != 1 || act[0].LastAt == nil {
		t.Fatalf("activity: %+v %v", act, err)
	}
	part, err := pr.RoomParticipation(ctx, f.inst, room.ID)
	if err != nil || len(part) != 2 {
		t.Fatalf("room participation: %+v %v", part, err)
	}
	for _, p := range part {
		if p.StudentID == f.virtualStudent && (p.ItemsSeen != 1 || p.Views != 2 || p.ActiveDays != 1) {
			t.Fatalf("participation of the active student: %+v", p)
		}
	}

	// Rule: default, then saved
	if r, _ := pr.Rule(ctx, f.inst); r.WindowDays != 7 || r.UpdatedAt != nil {
		t.Fatalf("default rule: %+v", r)
	}
	if err := pr.SaveRule(ctx, f.inst, domain.PermanenceRule{WindowDays: 7, MinActiveDays: 2, InactiveDays: 10, LowPercent: 50, UpdatedByName: "Dirección"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := pr.Rule(ctx, f.inst); r.MinActiveDays != 2 || r.UpdatedAt == nil {
		t.Fatalf("saved rule: %+v", r)
	}

	// Sections and students of the distance form
	secs, err := pr.DistanceSections(ctx, f.inst, f.period)
	if err != nil || len(secs) != 2 {
		t.Fatalf("distance sections: %+v %v", secs, err)
	}
	students, _ := pr.SectionStudents(ctx, f.inst, secs[0].ID)
	if len(students) != 1 {
		t.Fatalf("section students: %+v", students)
	}
	adj := domain.PermanenceAdjustment{EnrollmentID: students[0].EnrollmentID, WindowStart: time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), Present: true, Reason: "Llamada", ByName: "Tutor"}
	if err := pr.SaveAdjustment(ctx, f.inst, adj, false); err != nil {
		t.Fatal(err)
	}
	if list, _ := pr.Adjustments(ctx, []string{students[0].EnrollmentID}); len(list) != 1 || !list[0].Present {
		t.Fatalf("adjustments: %+v", list)
	}
	if err := pr.SaveAdjustment(ctx, f.inst, adj, true); err != nil {
		t.Fatal(err)
	}

	// Dropout signals: both students have the overdue task; only one has acted
	signals, err := pr.DropoutSignals(ctx, f.inst, f.period, time.Now())
	if err != nil || len(signals) != 2 {
		t.Fatalf("signals: %+v %v", signals, err)
	}
	for _, s := range signals {
		if s.Overdue != 1 {
			t.Fatalf("overdue: %+v", s)
		}
		if (s.StudentID == f.virtualStudent) != (s.LastAt != nil) {
			t.Fatalf("last action: %+v", s)
		}
	}
}

func mustForum(t *testing.T, r *ClassroomRepository, inst, unitID string) string {
	t.Helper()
	it, err := r.SaveItem(context.Background(), inst, "", domain.Item{UnitID: unitID, Kind: "forum", Title: "Foro", Published: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureForum(context.Background(), it.ID); err != nil {
		t.Fatal(err)
	}
	return it.ID
}
