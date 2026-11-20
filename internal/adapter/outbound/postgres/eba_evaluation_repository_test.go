package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"api/internal/core/domain"
)

// Runs against a migrated database only: LTEDU_TEST_DATABASE_URL=postgres://... go test ./internal/adapter/outbound/postgres/
func TestEBAEvaluationSQL(t *testing.T) {
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
		_, _ = c.pool.Exec(ctx, `DELETE FROM iam.institutions WHERE slug IN ('repo-test-eba-evaluation', 'repo-test-eba-evaluation-2')`)
	}
	cleanup()
	t.Cleanup(cleanup)
	q := func(dest *string, sql string, args ...any) {
		t.Helper()
		if err := c.pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var inst, other, level, g1, g2, p25, p26, section, ana, luis, eAna, eLuis, eAna25 string
	q(&inst, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-eba-evaluation', 'Repo Test EBA', 'eba') RETURNING id`)
	q(&other, `INSERT INTO iam.institutions (slug, name, kind) VALUES ('repo-test-eba-evaluation-2', 'Otro CEBA', 'eba') RETURNING id`)
	q(&level, `INSERT INTO academic.levels (institution_id, code, name, position, attention_modes) VALUES ($1, 'inicial', 'Ciclo Inicial', 0, '{in_person}') RETURNING id`, inst)
	q(&g1, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, '1', '1.° grado', '1.°', 0) RETURNING id`, level)
	q(&g2, `INSERT INTO academic.grades (level_id, code, name, short_name, position) VALUES ($1, '2', '2.° grado', '2.°', 1) RETURNING id`, level)
	q(&p25, `INSERT INTO academic.periods (institution_id, kind, name, starts_on, ends_on, term_scheme, status)
		VALUES ($1, 'promotional', 'PP 2025', '2025-03-17', '2025-12-12', 'bimester', 'closed') RETURNING id`, inst)
	q(&p26, `INSERT INTO academic.periods (institution_id, kind, name, starts_on, ends_on, term_scheme, status)
		VALUES ($1, 'promotional', 'PP 2026', '2026-03-16', '2026-12-11', 'bimester', 'active') RETURNING id`, inst)
	q(&section, `INSERT INTO academic.sections (period_id, level_id, grade_id, name, attention_mode, program, capacity, multigrade)
		VALUES ($1, $2, $3, 'A', 'in_person', 'pebaja', 30, true) RETURNING id`, p26, level, g1)
	var s25 string
	q(&s25, `INSERT INTO academic.sections (period_id, level_id, grade_id, name, attention_mode, program, capacity)
		VALUES ($1, $2, $3, 'A', 'in_person', 'pebaja', 30) RETURNING id`, p25, level, g1)
	q(&ana, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname) VALUES ($1, 'E-1', 'dni', '70000021', 'Ana', 'Ccori') RETURNING id`, inst)
	q(&luis, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname) VALUES ($1, 'E-2', 'dni', '70000022', 'Luis', 'Mamani') RETURNING id`, inst)
	q(&eAna25, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'new', '2025-03-17', 'completed') RETURNING id`, inst, ana, p25, level, g1, s25)
	q(&eAna, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on)
		VALUES ($1, $2, $3, $4, $5, $6, 'continuing', '2026-03-16') RETURNING id`, inst, ana, p26, level, g2, section)
	q(&eLuis, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on)
		VALUES ($1, $2, $3, $4, $5, $6, 'new', '2026-03-16') RETURNING id`, inst, luis, p26, level, g1, section)
	var x string
	q(&x, `INSERT INTO enrollment.religion_exemptions (enrollment_id, requested_on, reference) VALUES ($1, '2026-03-16', 'RD 012-2026') RETURNING enrollment_id`, eLuis)
	q(&x, `INSERT INTO assessment.final_results (institution_id, period_id, section_id, student_id, computed, confirmed_at)
		VALUES ($1, $2, $3, $4, 'PRO', now()) RETURNING id`, inst, p25, s25, ana)
	q(&x, `INSERT INTO assessment.final_results (institution_id, period_id, section_id, student_id, computed, after_recovery, confirmed_at)
		VALUES ($1, $2, $3, $4, 'RR', 'PRO', now()) RETURNING id`, inst, p26, section, ana)

	cl := NewClosingRepository(c)
	pr := NewPeopleRepository(c)

	// Promotion rules: the level with no rule, then a custom one; another institution cannot write it
	rules, err := cl.PromotionRules(ctx, inst)
	if err != nil || len(rules) != 1 || rules[0].Custom {
		t.Fatalf("rules: %+v %v", rules, err)
	}
	if err := cl.SavePromotionRule(ctx, other, domain.LevelRule{LevelID: level, Areas: 2}, "x"); err != domain.ErrNotFound {
		t.Fatalf("foreign level: %v", err)
	}
	if err := cl.SavePromotionRule(ctx, inst, domain.LevelRule{LevelID: level, Areas: 2, Note: "RVM 094-2020"}, "Directora"); err != nil {
		t.Fatal(err)
	}
	if err := cl.SavePromotionRule(ctx, inst, domain.LevelRule{LevelID: level, Areas: 3, Note: "RVM 094-2020"}, "Directora"); err != nil {
		t.Fatal(err)
	}
	if rules, _ = cl.PromotionRules(ctx, inst); !rules[0].Custom || rules[0].Areas != 3 || rules[0].UpdatedName != "Directora" {
		t.Fatalf("custom rule: %+v", rules[0])
	}
	if err := cl.ResetPromotionRule(ctx, inst, level); err != nil {
		t.Fatal(err)
	}
	if rules, _ = cl.PromotionRules(ctx, inst); rules[0].Custom {
		t.Fatal("reset kept the rule")
	}

	// What the closing reads of each student: own grade in the multigrade section, exemption, SIAGIE state
	info, err := cl.ClosingStudents(ctx, inst, section)
	if err != nil || len(info) != 2 {
		t.Fatalf("closing students: %+v %v", info, err)
	}
	if info[ana].GradeShort != "2.°" || info[ana].ReligionExempt || info[ana].SiagieStatus != domain.SiagiePending {
		t.Fatalf("ana: %+v", info[ana])
	}
	if info[luis].GradeShort != "1.°" || !info[luis].ReligionExempt {
		t.Fatalf("luis: %+v", info[luis])
	}
	at := time.Date(2026, 12, 15, 10, 0, 0, 0, time.UTC)
	if n, err := cl.SetSiagieGrades(ctx, other, []string{eAna, eLuis}, domain.SiagieExported, "", "x", at); err != nil || n != 0 {
		t.Fatalf("foreign siagie: %d %v", n, err)
	}
	if n, err := cl.SetSiagieGrades(ctx, inst, []string{eAna, eLuis}, domain.SiagieExported, "", "Secretaria", at); err != nil || n != 2 {
		t.Fatalf("siagie: %d %v", n, err)
	}
	if _, err := cl.SetSiagieGrades(ctx, inst, []string{eLuis}, domain.SiagieObserved, "", "Secretaria", at); err == nil {
		t.Fatal("observed without a note")
	}
	if _, err := cl.SetSiagieGrades(ctx, inst, []string{eLuis}, domain.SiagieObserved, "DNI no coincide", "Secretaria", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	info, _ = cl.ClosingStudents(ctx, inst, section)
	if info[luis].SiagieStatus != domain.SiagieObserved || info[luis].SiagieAt == nil || !info[luis].SiagieAt.Equal(at) {
		t.Fatalf("observed keeps the export date: %+v", info[luis])
	}

	// Studies: oldest first, the situation in force (after recovery) and the last grade of the cycle
	studies, err := pr.StudyRecords(ctx, inst, ana)
	if err != nil || len(studies) != 2 {
		t.Fatalf("studies: %+v %v", studies, err)
	}
	if studies[0].PeriodID != p25 || studies[0].Situation != "PRO" || studies[0].LastGrade || !studies[0].Confirmed {
		t.Fatalf("2025: %+v", studies[0])
	}
	if studies[1].Situation != "PRO" || !studies[1].LastGrade || studies[1].GradeShort != "2.°" {
		t.Fatalf("2026 after recovery: %+v", studies[1])
	}
	if s, _ := pr.StudyRecords(ctx, inst, luis); len(s) != 1 || s[0].Confirmed || s[0].Situation != "" {
		t.Fatalf("luis studies: %+v", s)
	}

	// Certificates are numbered per institution and year
	c1, err := pr.CreateCertificate(ctx, inst, domain.StudentCertificateInput{StudentID: ana, Kind: domain.CertificateCycle, LevelID: level, Purpose: "trámite laboral"}, 2026, "Secretaria")
	if err != nil || c1.Number != 1 || c1.LevelName != "Ciclo Inicial" || c1.StudentName != "Ccori, Ana" {
		t.Fatalf("certificate 1: %+v %v", c1, err)
	}
	c2, err := pr.CreateCertificate(ctx, inst, domain.StudentCertificateInput{StudentID: luis, Kind: domain.CertificateEnrollment, PeriodID: p26}, 2026, "Secretaria")
	if err != nil || c2.Number != 2 || c2.PeriodName != "PP 2026" {
		t.Fatalf("certificate 2: %+v %v", c2, err)
	}
	if c3, err := pr.CreateCertificate(ctx, inst, domain.StudentCertificateInput{StudentID: ana, Kind: domain.CertificateStudies}, 2027, "Secretaria"); err != nil || c3.Number != 1 {
		t.Fatalf("new year restarts: %+v %v", c3, err)
	}
	if _, err := pr.CreateCertificate(ctx, other, domain.StudentCertificateInput{StudentID: ana, Kind: domain.CertificateStudies}, 2026, "x"); err != domain.ErrNotFound {
		t.Fatalf("foreign student: %v", err)
	}
	if _, err := pr.CreateCertificate(ctx, inst, domain.StudentCertificateInput{StudentID: ana, Kind: domain.CertificateEnrollment}, 2026, "x"); err == nil {
		t.Fatal("an enrollment certificate needs its period")
	}
	list, err := pr.Certificates(ctx, inst, ana)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := pr.Certificate(ctx, other, c1.ID); err != domain.ErrNotFound {
		t.Fatalf("foreign certificate: %v", err)
	}
}
