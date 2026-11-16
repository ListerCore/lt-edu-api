package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
)

// Phase E7: promotion rules per cycle, what the closing reads of each student, the SIAGIE state of the grades, the
// signed actas and the certificates

// PromotionRules lists the active levels of the institution with the rule the CEBA set (Custom false when none)
func (r *ClosingRepository) PromotionRules(ctx context.Context, institutionID string) ([]domain.LevelRule, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT l.id, l.code, l.name, r.level_id IS NOT NULL, coalesce(r.automatic, false), coalesce(r.areas, 0),
			coalesce(r.note, ''), coalesce(r.updated_by_name, ''), r.updated_at
		FROM academic.levels l LEFT JOIN assessment.promotion_rules r ON r.level_id = l.id
		WHERE l.institution_id = $1 AND l.is_active ORDER BY l.position`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.LevelRule, error) {
		var x domain.LevelRule
		var areas int16
		err := row.Scan(&x.LevelID, &x.LevelCode, &x.LevelName, &x.Custom, &x.Automatic, &areas, &x.Note, &x.UpdatedName, &x.UpdatedAt)
		x.Areas = int(areas)
		return x, err
	})
}

// SavePromotionRule writes the rule of a level of the institution (domain.ErrNotFound when the level is not its own)
func (r *ClosingRepository) SavePromotionRule(ctx context.Context, institutionID string, x domain.LevelRule, by string) error {
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO assessment.promotion_rules (level_id, institution_id, automatic, areas, note, updated_by_name)
		SELECT l.id, l.institution_id, $3, $4, $5, $6 FROM academic.levels l WHERE l.institution_id = $1 AND l.id = $2
		ON CONFLICT (level_id) DO UPDATE SET automatic = EXCLUDED.automatic, areas = EXCLUDED.areas, note = EXCLUDED.note,
		updated_by_name = EXCLUDED.updated_by_name`, institutionID, x.LevelID, x.Automatic, x.Areas, x.Note, by)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ResetPromotionRule goes back to the rule of the code
func (r *ClosingRepository) ResetPromotionRule(ctx context.Context, institutionID, levelID string) error {
	_, err := r.c.pool.Exec(ctx, `DELETE FROM assessment.promotion_rules WHERE institution_id = $1 AND level_id = $2`, institutionID, levelID)
	return academicErr(err)
}

// ClosingStudents maps each active student of the section to their enrollment, grade, exemption and SIAGIE state
func (r *ClosingRepository) ClosingStudents(ctx context.Context, institutionID, sectionID string) (map[string]domain.ClosingStudent, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.student_id, e.id, coalesce(g.name, ''), coalesce(g.short_name, ''),
			EXISTS (SELECT 1 FROM enrollment.religion_exemptions x WHERE x.enrollment_id = e.id),
			coalesce(sg.status, 'pending'), coalesce(sg.note, ''), sg.exported_at
		FROM enrollment.enrollments e LEFT JOIN academic.grades g ON g.id = e.grade_id
		LEFT JOIN enrollment.siagie_grades sg ON sg.enrollment_id = e.id
		WHERE e.institution_id = $1 AND e.section_id = $2 AND e.status = 'active'`, institutionID, sectionID)
	if err != nil {
		return nil, academicErr(err)
	}
	defer rows.Close()
	out := map[string]domain.ClosingStudent{}
	for rows.Next() {
		var id string
		var x domain.ClosingStudent
		if err := rows.Scan(&id, &x.EnrollmentID, &x.GradeName, &x.GradeShort, &x.ReligionExempt, &x.SiagieStatus, &x.SiagieNote, &x.SiagieAt); err != nil {
			return nil, err
		}
		out[id] = x
	}
	return out, rows.Err()
}

// SetSiagieGrades sets the loading state of the grades of the enrollments of the institution; returns how many
func (r *ClosingRepository) SetSiagieGrades(ctx context.Context, institutionID string, enrollmentIDs []string, status, note, by string, at time.Time) (int, error) {
	var exported *time.Time
	if status == domain.SiagieExported {
		exported = &at
	}
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO enrollment.siagie_grades (enrollment_id, status, note, exported_at, updated_by_name)
		SELECT e.id, $3, $4, $5, $6 FROM enrollment.enrollments e WHERE e.institution_id = $1 AND e.id = ANY($2::uuid[])
		ON CONFLICT (enrollment_id) DO UPDATE SET status = EXCLUDED.status, note = EXCLUDED.note,
		exported_at = coalesce(EXCLUDED.exported_at, enrollment.siagie_grades.exported_at), updated_by_name = EXCLUDED.updated_by_name`,
		institutionID, enrollmentIDs, status, note, exported, by)
	return int(tag.RowsAffected()), academicErr(err)
}

// LinkRecord points the acta at the issued document of its signed version
func (r *ClosingRepository) LinkRecord(ctx context.Context, recordID, documentID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE assessment.records SET document_id = $2 WHERE id = $1`, recordID, documentID)
	return academicErr(err)
}

// ---------- studies and certificates ----------

// StudyRecords are the student's enrollments, oldest first, with the final situation in force
func (r *PeopleRepository) StudyRecords(ctx context.Context, institutionID, studentID string) ([]domain.StudyRecord, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.id, p.id, p.name, extract(year FROM p.starts_on)::int, l.id, l.name, coalesce(g.name, ''), coalesce(g.short_name, ''),
			coalesce(g.position = (SELECT max(g2.position) FROM academic.grades g2 WHERE g2.level_id = l.id), false),
			coalesce(sec.name, ''), coalesce(sec.attention_mode, ''), e.status,
			coalesce(nullif(fr.after_recovery, ''), nullif(fr.decided, ''), fr.computed, ''), coalesce(fr.confirmed_at IS NOT NULL, false)
		FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id JOIN academic.levels l ON l.id = e.level_id
		LEFT JOIN academic.grades g ON g.id = e.grade_id LEFT JOIN academic.sections sec ON sec.id = e.section_id
		LEFT JOIN assessment.final_results fr ON fr.period_id = e.period_id AND fr.student_id = e.student_id
		WHERE e.institution_id = $1 AND e.student_id = $2 ORDER BY p.starts_on, l.position, g.position`, institutionID, studentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.StudyRecord, error) {
		var x domain.StudyRecord
		err := row.Scan(&x.EnrollmentID, &x.PeriodID, &x.PeriodName, &x.Year, &x.LevelID, &x.LevelName, &x.GradeName, &x.GradeShort, &x.LastGrade,
			&x.SectionName, &x.AttentionMode, &x.Status, &x.Situation, &x.Confirmed)
		return x, err
	})
}

const certificateSelect = `SELECT c.id, c.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, c.kind,
	coalesce(c.period_id::text, ''), coalesce(p.name, ''), coalesce(c.level_id::text, ''), coalesce(l.name, ''), c.purpose, c.year, c.number,
	coalesce(c.document_id::text, ''), coalesce(v.id::text, ''), coalesce(v.status, ''), c.created_name, c.created_at
	FROM enrollment.certificates c JOIN people.students s ON s.id = c.student_id
	LEFT JOIN academic.periods p ON p.id = c.period_id LEFT JOIN academic.levels l ON l.id = c.level_id
	LEFT JOIN LATERAL (SELECT v.id, v.status FROM documents.versions v WHERE v.document_id = c.document_id ORDER BY v.number DESC LIMIT 1) v ON true`

func scanStudentCertificate(row pgx.Row) (domain.StudentCertificate, error) {
	var c domain.StudentCertificate
	var year int16
	var number int32
	err := row.Scan(&c.ID, &c.StudentID, &c.StudentName, &c.Kind, &c.PeriodID, &c.PeriodName, &c.LevelID, &c.LevelName, &c.Purpose, &year, &number,
		&c.DocumentID, &c.VersionID, &c.VersionState, &c.CreatedName, &c.CreatedAt)
	c.Year, c.Number = int(year), int(number)
	return c, err
}

// CreateCertificate numbers a new certificate in its year (one numbering per institution)
func (r *PeopleRepository) CreateCertificate(ctx context.Context, institutionID string, in domain.StudentCertificateInput, year int, by string) (domain.StudentCertificate, error) {
	var id string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('certificates:' || $1::text))`, institutionID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO enrollment.certificates (institution_id, student_id, kind, period_id, level_id, purpose, year, number, created_name)
			SELECT $1, s.id, $3, nullif($4, '')::uuid, nullif($5, '')::uuid, $6, $7,
				(SELECT coalesce(max(o.number), 0) + 1 FROM enrollment.certificates o WHERE o.institution_id = $1 AND o.year = $7), $8
			FROM people.students s WHERE s.institution_id = $1 AND s.id = $2 RETURNING id`,
			institutionID, in.StudentID, in.Kind, in.PeriodID, in.LevelID, in.Purpose, year, by).Scan(&id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StudentCertificate{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.StudentCertificate{}, academicErr(err)
	}
	return r.Certificate(ctx, institutionID, id)
}

func (r *PeopleRepository) LinkCertificate(ctx context.Context, id, documentID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE enrollment.certificates SET document_id = $2 WHERE id = $1`, id, documentID)
	return academicErr(err)
}

func (r *PeopleRepository) Certificate(ctx context.Context, institutionID, id string) (domain.StudentCertificate, error) {
	c, err := scanStudentCertificate(r.c.pool.QueryRow(ctx, certificateSelect+` WHERE c.institution_id = $1 AND c.id = $2`, institutionID, id))
	return c, academicErr(err)
}

// Certificates of a student, newest first
func (r *PeopleRepository) Certificates(ctx context.Context, institutionID, studentID string) ([]domain.StudentCertificate, error) {
	rows, err := r.c.pool.Query(ctx, certificateSelect+` WHERE c.institution_id = $1 AND c.student_id = $2 ORDER BY c.created_at DESC`, institutionID, studentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.StudentCertificate, error) { return scanStudentCertificate(row) })
}
