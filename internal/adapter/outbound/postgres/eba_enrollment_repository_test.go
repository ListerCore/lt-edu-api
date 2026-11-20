package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestEBAEnrollmentSQL(t *testing.T) {
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
		_, _ = c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug = 'repo-test-eba-enrollment'`)
	}
	cleanup()
	t.Cleanup(cleanup)
	q := func(dest *string, sql string, args ...any) {
		t.Helper()
		if err := c.pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var inst, level, g1, g2, period, curriculum, religion, section, ana, luis, eAna, eLuis string
	q(&inst, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-eba-enrollment', 'Repo Test EBA', 'eba') RETURNING id`)
	q(&level, `INSERT INTO academic.levels (institution_id, code, name, position, attention_modes) VALUES ($1, 'intermedio', 'Ciclo Intermedio', 0, '{in_person}') RETURNING id`, inst)
	q(&g1, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, '1', '1.° grado', '1.°', 0) RETURNING id`, level)
	q(&g2, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, '2', '2.° grado', '2.°', 1) RETURNING id`, level)
	q(&period, `INSERT INTO academic.periods (institution_id, kind, name, starts_on, ends_on, term_scheme, status)
		VALUES ($1, 'promotional', 'PP 2026', '2026-03-16', '2026-12-11', 'bimester', 'active') RETURNING id`, inst)
	q(&curriculum, `INSERT INTO academic.curricula (institution_id, level_id, name, status) VALUES ($1, $2, 'Plan EBA', 'active') RETURNING id`, inst, level)
	q(&religion, `INSERT INTO academic.subjects (curriculum_id, kind, code, name, position) VALUES ($1, 'area', 'educacion-religiosa', 'Educación Religiosa', 0) RETURNING id`, curriculum)
	q(&section, `INSERT INTO academic.sections (period_id, level_id, grade_id, name, attention_mode, program, capacity, multigrade)
		VALUES ($1, $2, $3, 'A', 'in_person', 'pebaja', 30, true) RETURNING id`, period, level, g1)
	q(&ana, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname, birth_date)
		VALUES ($1, 'E-1', 'dni', '70000011', 'Ana', 'Ccori', '1990-05-02') RETURNING id`, inst)
	q(&luis, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname)
		VALUES ($1, 'E-2', 'dni', '70000012', 'Luis', 'Mamani') RETURNING id`, inst)
	q(&eAna, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on)
		VALUES ($1, $2, $3, $4, $5, $6, 'new', '2026-03-16') RETURNING id`, inst, ana, period, level, g1, section)
	q(&eLuis, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on)
		VALUES ($1, $2, $3, $4, $5, $6, 'transfer_in', '2026-03-16') RETURNING id`, inst, luis, period, level, g2, section)

	r := NewPeopleRepository(c)
	ac := NewAcademicRepository(c)
	if n, err := ac.OtherGrades(ctx, section); err != nil || n != 1 {
		t.Fatalf("other grades in the multigrade section: %d %v", n, err)
	}

	// Ficha: defaults, then saved
	f, err := r.EnrollmentFile(ctx, inst, ana)
	if err != nil || f.MotherTongue != "castellano" || f.UpdatedAt != nil {
		t.Fatalf("default file: %+v %v", f, err)
	}
	f.MotherTongue, f.Occupation, f.Internet, f.Device, f.PreferredMode, f.Documents, f.UpdatedByName = "quechua", "Comerciante", "mobile", "phone", "blended", []string{"dni"}, "Secretaría"
	if err := r.SaveEnrollmentFile(ctx, inst, f); err != nil {
		t.Fatal(err)
	}
	if f, _ = r.EnrollmentFile(ctx, inst, ana); f.MotherTongue != "quechua" || f.PreferredMode != "blended" || len(f.Documents) != 1 || f.UpdatedAt == nil {
		t.Fatalf("saved file: %+v", f)
	}
	f.Documents = []string{"passport_photo"}
	if err := r.SaveEnrollmentFile(ctx, inst, f); err == nil {
		t.Fatal("unknown document kind accepted")
	}

	// Religion: the plan has it, the exempt student leaves the register
	if ok, err := r.HasReligion(ctx, inst, eAna); err != nil || !ok {
		t.Fatalf("has religion: %v %v", ok, err)
	}
	if err := r.SetReligionExemption(ctx, inst, domain.ReligionExemption{EnrollmentID: eAna, RequestedOn: time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC), Reference: "RD 012-2026"}); err != nil {
		t.Fatal(err)
	}
	students, err := NewAssessmentRepository(c).Students(ctx, inst, section, religion)
	if err != nil || len(students) != 1 || students[0].ID != luis {
		t.Fatalf("religion register without the exempt student: %+v %v", students, err)
	}

	// SIAGIE: observed needs a note; exported stamps the time
	if _, err := r.SetSiagie(ctx, inst, []string{eLuis}, domain.SiagieObserved, "", "Secretaría", time.Now()); err == nil {
		t.Fatal("observed without a note accepted")
	}
	if n, err := r.SetSiagie(ctx, inst, []string{eAna, eLuis}, domain.SiagieExported, "", "Secretaría", time.Now()); err != nil || n != 2 {
		t.Fatalf("exported: %d %v", n, err)
	}
	roll, err := r.Roll(ctx, inst, domain.RollFilter{PeriodID: period})
	if err != nil || len(roll) != 2 {
		t.Fatalf("roll: %+v %v", roll, err)
	}
	if !roll[0].ReligionExempt || roll[0].SiagieStatus != domain.SiagieExported || roll[0].SiagieExported == nil || roll[0].MotherTongue != "quechua" || roll[1].GradePosition != 1 {
		t.Fatalf("roll rows: %+v", roll)
	}
	if pending, _ := r.Roll(ctx, inst, domain.RollFilter{PeriodID: period, SiagieStatus: domain.SiagiePending}); len(pending) != 0 {
		t.Fatalf("pending after export: %d", len(pending))
	}

	// Placement with validated areas and its acta number, once per placement and consecutive per year
	p, err := r.CreatePlacement(ctx, inst, domain.PlacementInput{StudentID: luis, Kind: domain.PlacementValidation, LevelID: level, GradeID: g2,
		DecidedOn: time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC), Origin: "IE 38001", OriginStudies: "3.° de primaria, EBR", SubjectIDs: []string{religion}})
	if err != nil || len(p.SubjectNames) != 1 || p.Origin != "IE 38001" || p.ActNumber != 0 {
		t.Fatalf("placement: %+v %v", p, err)
	}
	y, n, err := r.AssignAct(ctx, inst, p.ID, 2026)
	if err != nil || y != 2026 || n != 1 {
		t.Fatalf("act: %d %d %v", y, n, err)
	}
	if _, n2, _ := r.AssignAct(ctx, inst, p.ID, 2027); n2 != 1 {
		t.Fatalf("act number changed: %d", n2)
	}
	other, _ := r.CreatePlacement(ctx, inst, domain.PlacementInput{StudentID: ana, Kind: domain.PlacementTest, LevelID: level, GradeID: g1, DecidedOn: time.Now()})
	if _, n3, _ := r.AssignAct(ctx, inst, other.ID, 2026); n3 != 2 {
		t.Fatalf("second act of the year: %d", n3)
	}
	if roll, _ = r.Roll(ctx, inst, domain.RollFilter{PeriodID: period}); roll[1].Placement != "Convalidación, acta N.° 001-2026" {
		t.Fatalf("placement in the roll: %q", roll[1].Placement)
	}
}
