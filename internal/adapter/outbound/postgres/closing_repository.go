package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ClosingRepository implements port.ClosingRepository
type ClosingRepository struct{ c *Client }

func NewClosingRepository(c *Client) *ClosingRepository { return &ClosingRepository{c: c} }

var _ port.ClosingRepository = (*ClosingRepository)(nil)

func (r *ClosingRepository) SectionLevel(ctx context.Context, institutionID, sectionID string) (domain.SectionLevel, error) {
	var s domain.SectionLevel
	err := r.c.pool.QueryRow(ctx, `SELECT l.code, l.name, coalesce(g.code, ''), coalesce(g.short_name, '')
		FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id WHERE p.institution_id = $1 AND sec.id = $2`, institutionID, sectionID).
		Scan(&s.LevelCode, &s.LevelName, &s.GradeCode, &s.GradeShort)
	return s, academicErr(err)
}

func (r *ClosingRepository) FinalResults(ctx context.Context, institutionID, periodID, sectionID string) ([]domain.FinalResult, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT student_id, section_id, computed, reason, decided, decision_note, decided_name, decided_at, confirmed_at, after_recovery, recovery_at
		FROM assessment.final_results WHERE institution_id = $1 AND period_id = $2 AND ($3 = '' OR section_id = nullif($3, '')::uuid)`, institutionID, periodID, sectionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.FinalResult, error) {
		var f domain.FinalResult
		err := row.Scan(&f.StudentID, &f.SectionID, &f.Computed, &f.Reason, &f.Decided, &f.DecisionNote, &f.DecidedName, &f.DecidedAt, &f.ConfirmedAt, &f.AfterRecovery, &f.RecoveryAt)
		return f, err
	})
}

func (r *ClosingRepository) SaveDecision(ctx context.Context, institutionID, periodID, sectionID, studentID, decided, note, name string) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO assessment.final_results (institution_id, period_id, section_id, student_id, decided, decision_note, decided_name, decided_at)
		VALUES ($1, $2, $3, $4, $5::text, $6, $7, CASE WHEN $5::text = '' THEN NULL ELSE now() END)
		ON CONFLICT (period_id, student_id) DO UPDATE SET decided = EXCLUDED.decided, decision_note = EXCLUDED.decision_note,
			decided_name = EXCLUDED.decided_name, decided_at = EXCLUDED.decided_at, section_id = EXCLUDED.section_id,
			-- A new situation invalidates the recovery result
			after_recovery = CASE WHEN EXCLUDED.decided = 'RR' OR (EXCLUDED.decided = '' AND assessment.final_results.computed = 'RR') THEN assessment.final_results.after_recovery ELSE '' END`,
		institutionID, periodID, sectionID, studentID, decided, note, name)
	return academicErr(err)
}

func (r *ClosingRepository) ConfirmResults(ctx context.Context, institutionID, periodID, sectionID string, rows []port.FinalRow) error {
	batch := &pgx.Batch{}
	for _, x := range rows {
		batch.Queue(`INSERT INTO assessment.final_results (institution_id, period_id, section_id, student_id, computed, reason, confirmed_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())
			ON CONFLICT (period_id, student_id) DO UPDATE SET computed = EXCLUDED.computed, reason = EXCLUDED.reason, section_id = EXCLUDED.section_id, confirmed_at = now()`,
			institutionID, periodID, sectionID, x.StudentID, x.Computed, x.Reason)
	}
	return academicErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *ClosingRepository) RecoveryLevels(ctx context.Context, periodID string, studentIDs []string) (map[string]map[string]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT student_id, competency_id, level FROM assessment.recovery_levels WHERE period_id = $1 AND student_id = ANY($2::uuid[])`, periodID, studentIDs)
	if err != nil {
		return nil, academicErr(err)
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var s, c, l string
		if err := rows.Scan(&s, &c, &l); err != nil {
			return nil, err
		}
		if out[s] == nil {
			out[s] = map[string]string{}
		}
		out[s][c] = l
	}
	return out, academicErr(rows.Err())
}

func (r *ClosingRepository) SaveRecovery(ctx context.Context, periodID, studentID string, levels map[string]string, after string) error {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for comp, level := range levels {
		if level == "" {
			_, err = tx.Exec(ctx, `DELETE FROM assessment.recovery_levels WHERE period_id = $1 AND student_id = $2 AND competency_id = $3`, periodID, studentID, comp)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO assessment.recovery_levels (period_id, student_id, competency_id, level) VALUES ($1, $2, $3, $4)
				ON CONFLICT (period_id, student_id, competency_id) DO UPDATE SET level = EXCLUDED.level, updated_at = now()`, periodID, studentID, comp, level)
		}
		if err != nil {
			return academicErr(err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE assessment.final_results SET after_recovery = $3::text, recovery_at = CASE WHEN $3::text = '' THEN NULL ELSE now() END
		WHERE period_id = $1 AND student_id = $2`, periodID, studentID, after); err != nil {
		return academicErr(err)
	}
	return tx.Commit(ctx)
}

func (r *ClosingRepository) UnitResults(ctx context.Context, institutionID, assignmentID string) ([]domain.UnitResult, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT student_id, average::float8, grade, absent_percent::float8, status, recovery_score, final_grade, confirmed_at
		FROM assessment.unit_results WHERE institution_id = $1 AND assignment_id = $2`, institutionID, assignmentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UnitResult, error) {
		var u domain.UnitResult
		var grade, rec, final *int16
		err := row.Scan(&u.StudentID, &u.Average, &grade, &u.AbsentPercent, &u.Status, &rec, &final, &u.ConfirmedAt)
		u.Grade, u.RecoveryScore, u.FinalGrade = intOf(grade), intOf(rec), intOf(final)
		return u, err
	})
}

func intOf(v *int16) *int {
	if v == nil {
		return nil
	}
	x := int(*v)
	return &x
}

func (r *ClosingRepository) ConfirmUnit(ctx context.Context, institutionID, assignmentID string, rows []domain.UnitResult) error {
	batch := &pgx.Batch{}
	for _, u := range rows {
		// Re-confirming recomputes the unit but keeps a recovery already given
		batch.Queue(`INSERT INTO assessment.unit_results (institution_id, assignment_id, student_id, average, grade, absent_percent, status, final_grade)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $5)
			ON CONFLICT (assignment_id, student_id) DO UPDATE SET average = EXCLUDED.average, grade = EXCLUDED.grade, absent_percent = EXCLUDED.absent_percent,
				status = CASE WHEN assessment.unit_results.recovery_score IS NOT NULL AND EXCLUDED.status = 'recovery' THEN assessment.unit_results.status ELSE EXCLUDED.status END,
				final_grade = CASE WHEN assessment.unit_results.recovery_score IS NOT NULL AND EXCLUDED.status = 'recovery' THEN assessment.unit_results.final_grade ELSE EXCLUDED.grade END,
				recovery_score = CASE WHEN EXCLUDED.status = 'recovery' THEN assessment.unit_results.recovery_score END,
				confirmed_at = now()`,
			institutionID, assignmentID, u.StudentID, u.Average, u.Grade, u.AbsentPercent, u.Status)
	}
	return academicErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *ClosingRepository) SaveUnitRecovery(ctx context.Context, assignmentID, studentID string, score *int, final *int, status string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE assessment.unit_results SET recovery_score = $3, final_grade = coalesce($4, grade), status = $5
		WHERE assignment_id = $1 AND student_id = $2`, assignmentID, studentID, score, final, status)
	return academicErr(err)
}

var recordPrefix = map[string]string{"final": "AEF", "complementary": "AEC", "unit": "AUD"}

func (r *ClosingRepository) IssueRecord(ctx context.Context, institutionID string, rec domain.Record, userID string) (domain.Record, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return domain.Record{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// One numbering per period and kind; the lock keeps two issues from taking the same number
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || $2))`, rec.PeriodID, rec.Kind); err != nil {
		return domain.Record{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE assessment.records SET status = 'replaced' WHERE institution_id = $1 AND section_id = $2 AND kind = $3
		AND assignment_id IS NOT DISTINCT FROM nullif($4, '')::uuid AND status = 'issued'`, institutionID, rec.SectionID, rec.Kind, rec.AssignmentID); err != nil {
		return domain.Record{}, academicErr(err)
	}
	var year int
	if err := tx.QueryRow(ctx, `SELECT extract(year FROM starts_on)::int FROM academic.periods WHERE id = $1`, rec.PeriodID).Scan(&year); err != nil {
		return domain.Record{}, academicErr(err)
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO assessment.records (institution_id, period_id, section_id, assignment_id, kind, number, code, content, issued_by, issued_name)
		SELECT $1, $2, $3, nullif($4, '')::uuid, $5, n, $6 || '-' || $7 || '-' || lpad(n::text, 3, '0'), $8, nullif($9, '')::uuid, $10
		FROM (SELECT coalesce(max(number), 0) + 1 AS n FROM assessment.records WHERE institution_id = $1 AND period_id = $2 AND kind = $5) x
		RETURNING id`, institutionID, rec.PeriodID, rec.SectionID, rec.AssignmentID, rec.Kind, recordPrefix[rec.Kind], fmt.Sprint(year), rec.Content, userID, rec.IssuedName).Scan(&id)
	if err != nil {
		return domain.Record{}, academicErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Record{}, err
	}
	return r.Record(ctx, institutionID, id)
}

const recordColumns = `r.id, r.period_id, r.section_id, coalesce(g.short_name || ' ', '') || sec.name || ' · ' || l.name, coalesce(r.assignment_id::text, ''),
	r.kind, r.number, r.code, r.status, r.issued_name, r.issued_at, coalesce(r.document_id::text, ''), coalesce(v.id::text, ''), coalesce(v.status, '')`

const recordFrom = ` FROM assessment.records r JOIN academic.sections sec ON sec.id = r.section_id JOIN academic.levels l ON l.id = sec.level_id
	LEFT JOIN academic.grades g ON g.id = sec.grade_id
	LEFT JOIN LATERAL (SELECT v.id, v.status FROM documents.versions v WHERE v.document_id = r.document_id ORDER BY v.number DESC LIMIT 1) v ON true`

func scanRecord(row pgx.Row, withContent bool) (domain.Record, error) {
	var x domain.Record
	dest := []any{&x.ID, &x.PeriodID, &x.SectionID, &x.SectionLabel, &x.AssignmentID, &x.Kind, &x.Number, &x.Code, &x.Status, &x.IssuedName, &x.IssuedAt, &x.DocumentID, &x.ActVersionID, &x.ActStatus}
	if withContent {
		dest = append(dest, &x.Content)
	}
	err := row.Scan(dest...)
	return x, err
}

func (r *ClosingRepository) Records(ctx context.Context, institutionID, periodID string) ([]domain.Record, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+recordColumns+recordFrom+` WHERE r.institution_id = $1 AND r.period_id = $2 ORDER BY r.kind, r.number DESC`, institutionID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Record, error) { return scanRecord(row, false) })
}

func (r *ClosingRepository) Record(ctx context.Context, institutionID, id string) (domain.Record, error) {
	x, err := scanRecord(r.c.pool.QueryRow(ctx, `SELECT `+recordColumns+`, r.content`+recordFrom+` WHERE r.institution_id = $1 AND r.id = $2`, institutionID, id), true)
	return x, academicErr(err)
}
