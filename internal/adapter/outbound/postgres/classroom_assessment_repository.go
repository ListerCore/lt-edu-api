package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
)

// Online assessments of the virtual classroom: classroom.assessments, questions, submissions and extensions

func (r *ClassroomRepository) EnsureAssessment(ctx context.Context, itemID string) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO classroom.assessments (item_id) VALUES ($1) ON CONFLICT DO NOTHING`, itemID)
	return academicErr(err)
}

func (r *ClassroomRepository) Assessment(ctx context.Context, institutionID, itemID string) (domain.Assessment, error) {
	var a domain.Assessment
	var attempts int16
	err := r.c.pool.QueryRow(ctx, `SELECT i.id, i.room_id, i.unit_id, i.kind, i.title, i.body, i.published, s.opens_at, s.closes_at, s.max_attempts,
		s.competency_ids::text[], s.show_answers
		FROM classroom.assessments s JOIN classroom.items i ON i.id = s.item_id JOIN classroom.rooms r ON r.id = i.room_id
		WHERE r.institution_id = $1 AND s.item_id = $2`, institutionID, itemID).
		Scan(&a.ItemID, &a.RoomID, &a.UnitID, &a.Kind, &a.Title, &a.Instructions, &a.Published, &a.OpensAt, &a.ClosesAt, &attempts, &a.CompetencyIDs, &a.ShowAnswers)
	if err != nil {
		return a, academicErr(err)
	}
	a.MaxAttempts = int(attempts)
	rows, err := r.c.pool.Query(ctx, `SELECT id, position, kind, prompt, options, correct, accepted, points FROM classroom.questions
		WHERE item_id = $1 ORDER BY position`, itemID)
	if err != nil {
		return a, academicErr(err)
	}
	a.Questions, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.QuizQuestion, error) {
		var q domain.QuizQuestion
		var pos, points int16
		var correct []int16
		err := row.Scan(&q.ID, &pos, &q.Kind, &q.Prompt, &q.Options, &correct, &q.Accepted, &points)
		q.Position, q.Points = int(pos), int(points)
		for _, c := range correct {
			q.Correct = append(q.Correct, int(c))
		}
		return q, err
	})
	if a.Questions == nil {
		a.Questions = []domain.QuizQuestion{}
	}
	return a, academicErr(err)
}

func (r *ClassroomRepository) SaveAssessment(ctx context.Context, institutionID string, a domain.Assessment, questions []domain.QuizQuestion) (domain.Assessment, error) {
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		// The competencies must be of the room's area
		tag, err := tx.Exec(ctx, `UPDATE classroom.assessments s SET opens_at = $3, closes_at = $4, max_attempts = $5, competency_ids = $6::uuid[], show_answers = $7
			FROM classroom.items i, classroom.rooms r
			WHERE i.id = s.item_id AND r.id = i.room_id AND r.institution_id = $1 AND s.item_id = $2
			  AND NOT EXISTS (SELECT 1 FROM unnest($6::uuid[]) c WHERE c NOT IN (SELECT id FROM academic.competencies WHERE subject_id = r.subject_id))`,
			institutionID, a.ItemID, a.OpensAt, a.ClosesAt, a.MaxAttempts, a.CompetencyIDs, a.ShowAnswers)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		if questions == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `DELETE FROM classroom.questions WHERE item_id = $1`, a.ItemID); err != nil {
			return err
		}
		for i, q := range questions {
			correct := make([]int16, len(q.Correct))
			for j, c := range q.Correct {
				correct[j] = int16(c)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO classroom.questions (item_id, position, kind, prompt, options, correct, accepted, points)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, a.ItemID, i, q.Kind, q.Prompt, nonNil(q.Options), correct, nonNil(q.Accepted), q.Points); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.Assessment{}, err
		}
		return domain.Assessment{}, academicErr(err)
	}
	return r.Assessment(ctx, institutionID, a.ItemID)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *ClassroomRepository) HandedIn(ctx context.Context, itemID string) (int, error) {
	var n int
	err := r.c.pool.QueryRow(ctx, `SELECT count(*) FROM classroom.submissions WHERE item_id = $1 AND status <> 'in_progress'`, itemID).Scan(&n)
	return n, academicErr(err)
}

func (r *ClassroomRepository) RoomCompetencies(ctx context.Context, institutionID, roomID string) ([]domain.CompetencyOption, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT c.id::text, c.name FROM academic.competencies c JOIN classroom.rooms r ON r.subject_id = c.subject_id
		WHERE r.institution_id = $1 AND r.id = $2 ORDER BY c.position`, institutionID, roomID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.CompetencyOption, error) {
		var c domain.CompetencyOption
		err := row.Scan(&c.ID, &c.Name)
		return c, err
	})
	if out == nil {
		out = []domain.CompetencyOption{}
	}
	return out, academicErr(err)
}

// ---------- submissions ----------

const quizSubmissionColumns = `s.id, s.item_id, s.student_id, st.paternal_surname || CASE WHEN st.maternal_surname <> '' THEN ' ' || st.maternal_surname ELSE '' END || ', ' || st.given_names,
	st.code, s.attempt, s.status, s.answers, s.auto_points, s.auto_total, s.open_points, s.levels, s.feedback, s.started_at, s.submitted_at, s.late,
	s.graded_at, s.graded_name, s.recorded_at,
	(SELECT count(*) FROM attachments.files f WHERE f.owner_kind = 'submission' AND f.owner_id = s.id)`

func scanQuizSubmission(row pgx.Row) (domain.QuizSubmission, error) {
	var s domain.QuizSubmission
	var attempt, autoPoints, autoTotal int16
	var answers, openPoints, levels []byte
	var files int64
	err := row.Scan(&s.ID, &s.ItemID, &s.StudentID, &s.StudentName, &s.StudentCode, &attempt, &s.Status, &answers, &autoPoints, &autoTotal, &openPoints, &levels,
		&s.Feedback, &s.StartedAt, &s.SubmittedAt, &s.Late, &s.GradedAt, &s.GradedName, &s.RecordedAt, &files)
	if err != nil {
		return s, err
	}
	s.Attempt, s.AutoPoints, s.AutoTotal, s.Files = int(attempt), int(autoPoints), int(autoTotal), int(files)
	s.Answers, s.OpenPoints, s.Levels = map[string]domain.QuizAnswer{}, map[string]int{}, map[string]string{}
	_ = json.Unmarshal(answers, &s.Answers)
	_ = json.Unmarshal(openPoints, &s.OpenPoints)
	_ = json.Unmarshal(levels, &s.Levels)
	return s, nil
}

func (r *ClassroomRepository) Roster(ctx context.Context, institutionID, itemID string) ([]domain.QuizRosterRow, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT st.id::text,
		st.paternal_surname || CASE WHEN st.maternal_surname <> '' THEN ' ' || st.maternal_surname ELSE '' END || ', ' || st.given_names, st.code,
		coalesce(e.section_id::text, ''),
		(SELECT id::text FROM classroom.submissions WHERE item_id = i.id AND student_id = st.id ORDER BY attempt DESC LIMIT 1),
		(SELECT count(*) FROM classroom.submissions WHERE item_id = i.id AND student_id = st.id),
		(SELECT closes_at FROM classroom.extensions WHERE item_id = i.id AND student_id = st.id)
		FROM classroom.items i
		JOIN classroom.rooms r ON r.id = i.room_id
		JOIN enrollment.enrollments e ON e.period_id = r.period_id AND e.status = 'active' AND e.grade_id IS NOT DISTINCT FROM r.grade_id
		JOIN academic.subjects sub ON sub.id = r.subject_id
		JOIN academic.curricula c ON c.id = sub.curriculum_id AND c.level_id = e.level_id
		JOIN people.students st ON st.id = e.student_id
		WHERE r.institution_id = $1 AND i.id = $2
		ORDER BY st.paternal_surname, st.maternal_surname, st.given_names`, institutionID, itemID)
	if err != nil {
		return nil, academicErr(err)
	}
	type raw struct {
		row    domain.QuizRosterRow
		latest *string
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (raw, error) {
		var x raw
		var attempts int64
		err := row.Scan(&x.row.StudentID, &x.row.StudentName, &x.row.StudentCode, &x.row.SectionID, &x.latest, &attempts, &x.row.Extension)
		x.row.Attempts = int(attempts)
		return x, err
	})
	if err != nil {
		return nil, academicErr(err)
	}
	out := make([]domain.QuizRosterRow, 0, len(list))
	for _, x := range list {
		if x.latest != nil {
			s, err := r.QuizSubmission(ctx, institutionID, *x.latest)
			if err != nil {
				return nil, err
			}
			x.row.Latest = &s
		}
		out = append(out, x.row)
	}
	return out, nil
}

func (r *ClassroomRepository) Attempts(ctx context.Context, itemID, studentID string) ([]domain.QuizSubmission, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+quizSubmissionColumns+` FROM classroom.submissions s JOIN people.students st ON st.id = s.student_id
		WHERE s.item_id = $1 AND s.student_id = $2 ORDER BY s.attempt DESC`, itemID, studentID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.QuizSubmission, error) { return scanQuizSubmission(row) })
	if out == nil {
		out = []domain.QuizSubmission{}
	}
	return out, academicErr(err)
}

func (r *ClassroomRepository) QuizSubmission(ctx context.Context, institutionID, id string) (domain.QuizSubmission, error) {
	s, err := scanQuizSubmission(r.c.pool.QueryRow(ctx, `SELECT `+quizSubmissionColumns+` FROM classroom.submissions s
		JOIN people.students st ON st.id = s.student_id
		WHERE st.institution_id = $1 AND s.id = $2`, institutionID, id))
	return s, academicErr(err)
}

func (r *ClassroomRepository) CreateAttempt(ctx context.Context, itemID, studentID string, attempt int) (domain.QuizSubmission, error) {
	var id, inst string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO classroom.submissions (item_id, student_id, attempt) VALUES ($1, $2, $3)
		RETURNING id, (SELECT institution_id::text FROM people.students WHERE id = $2)`, itemID, studentID, attempt).Scan(&id, &inst)
	if err != nil {
		return domain.QuizSubmission{}, academicErr(err)
	}
	return r.QuizSubmission(ctx, inst, id)
}

func (r *ClassroomRepository) SaveAnswers(ctx context.Context, id string, answers map[string]domain.QuizAnswer) error {
	b, err := json.Marshal(answers)
	if err != nil {
		return err
	}
	tag, err := r.c.pool.Exec(ctx, `UPDATE classroom.submissions SET answers = $2 WHERE id = $1 AND status = 'in_progress'`, id, b)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrAttemptFinished
	}
	return academicErr(err)
}

func (r *ClassroomRepository) HandIn(ctx context.Context, id string, autoPoints, autoTotal int, late bool, at time.Time) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE classroom.submissions SET status = 'submitted', auto_points = $2, auto_total = $3, late = $4, submitted_at = $5
		WHERE id = $1 AND status = 'in_progress'`, id, autoPoints, autoTotal, late, at)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrAttemptFinished
	}
	return academicErr(err)
}

func (r *ClassroomRepository) Grade(ctx context.Context, institutionID string, s domain.QuizSubmission, userID, userName string) (domain.QuizSubmission, error) {
	levels, err := json.Marshal(s.Levels)
	if err != nil {
		return s, err
	}
	open, err := json.Marshal(s.OpenPoints)
	if err != nil {
		return s, err
	}
	tag, err := r.c.pool.Exec(ctx, `UPDATE classroom.submissions sb SET status = 'graded', levels = $3, open_points = $4, feedback = $5, graded_at = now(),
		graded_by = $6::uuid, graded_name = $7, recorded_at = NULL
		FROM people.students st WHERE st.id = sb.student_id AND st.institution_id = $1 AND sb.id = $2 AND sb.status <> 'in_progress'`,
		institutionID, s.ID, levels, open, s.Feedback, nilIfEmpty(userID), userName)
	if err != nil {
		return s, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return s, domain.ErrNotFound
	}
	return r.QuizSubmission(ctx, institutionID, s.ID)
}

func (r *ClassroomRepository) Extension(ctx context.Context, itemID, studentID string) (*time.Time, error) {
	var t time.Time
	err := r.c.pool.QueryRow(ctx, `SELECT closes_at FROM classroom.extensions WHERE item_id = $1 AND student_id = $2`, itemID, studentID).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, academicErr(err)
	}
	return &t, nil
}

func (r *ClassroomRepository) SetExtension(ctx context.Context, itemID, studentID string, closesAt *time.Time, reason, grantedName string) error {
	if closesAt == nil {
		_, err := r.c.pool.Exec(ctx, `DELETE FROM classroom.extensions WHERE item_id = $1 AND student_id = $2`, itemID, studentID)
		return academicErr(err)
	}
	_, err := r.c.pool.Exec(ctx, `INSERT INTO classroom.extensions (item_id, student_id, closes_at, reason, granted_name) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (item_id, student_id) DO UPDATE SET closes_at = EXCLUDED.closes_at, reason = EXCLUDED.reason, granted_name = EXCLUDED.granted_name`,
		itemID, studentID, *closesAt, reason, grantedName)
	return academicErr(err)
}

// ---------- official record ----------

func (r *ClassroomRepository) RecordTargets(ctx context.Context, institutionID, itemID string) ([]domain.RecordTarget, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT DISTINCT ON (s.student_id) s.id::text, s.student_id::text, coalesce(e.section_id::text, ''),
		coalesce((SELECT a.id::text FROM academic.assignments a WHERE a.section_id = e.section_id AND a.subject_id = rm.subject_id), ''),
		s.levels, s.feedback
		FROM classroom.submissions s
		JOIN classroom.items i ON i.id = s.item_id
		JOIN classroom.rooms rm ON rm.id = i.room_id
		JOIN enrollment.enrollments e ON e.student_id = s.student_id AND e.period_id = rm.period_id AND e.status = 'active'
		WHERE rm.institution_id = $1 AND s.item_id = $2 AND s.status = 'graded' AND s.levels <> '{}'::jsonb
		ORDER BY s.student_id, s.attempt DESC`, institutionID, itemID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.RecordTarget, error) {
		var t domain.RecordTarget
		var levels []byte
		err := row.Scan(&t.SubmissionID, &t.StudentID, &t.SectionID, &t.AssignmentID, &levels, &t.Feedback)
		t.Levels = map[string]string{}
		_ = json.Unmarshal(levels, &t.Levels)
		return t, err
	})
	return out, academicErr(err)
}

func (r *ClassroomRepository) OpenTerm(ctx context.Context, periodID string, day time.Time) (string, error) {
	var id string
	// The open term that contains the day or, between terms, the last open one that already started
	err := r.c.pool.QueryRow(ctx, `SELECT id::text FROM academic.terms WHERE period_id = $1 AND status = 'open' AND starts_on <= $2
		ORDER BY (ends_on >= $2) DESC, starts_on DESC LIMIT 1`, periodID, date(day)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, academicErr(err)
}

func (r *ClassroomRepository) MarkRecorded(ctx context.Context, ids []string, at time.Time) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE classroom.submissions SET recorded_at = $2 WHERE id = ANY($1::uuid[])`, ids, at)
	return academicErr(err)
}
