package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ExamsRepository implements port.ExamsRepository
type ExamsRepository struct{ c *Client }

func NewExamsRepository(c *Client) *ExamsRepository { return &ExamsRepository{c: c} }

var _ port.ExamsRepository = (*ExamsRepository)(nil)

func examsErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "admissions_key" {
		return domain.Validation(map[string]string{"exam": "Ya está registrado como ingresante en ese examen."})
	}
	return academicErr(err)
}

func (r *ExamsRepository) Courses(ctx context.Context, institutionID string) ([]domain.Course, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.code, min(s.name),
			jsonb_object_agg(cu.level_id::text, coalesce(s.weekly_hours, 0)),
			coalesce((SELECT array_agg(DISTINCT c.name ORDER BY c.name) FROM academic.competencies c JOIN academic.subjects s2 ON s2.id = c.subject_id
				JOIN academic.curricula cu2 ON cu2.id = s2.curriculum_id WHERE cu2.institution_id = $1 AND cu2.status = 'active' AND s2.code = s.code), '{}'),
			(SELECT count(*) FROM exams.questions q WHERE q.institution_id = $1 AND q.course_code = s.code)
		FROM academic.subjects s JOIN academic.curricula cu ON cu.id = s.curriculum_id
		WHERE cu.institution_id = $1 AND cu.status = 'active' AND s.kind = 'course'
		GROUP BY s.code ORDER BY min(s.position), min(s.name)`, institutionID)
	if err != nil {
		return nil, examsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Course, error) {
		var c domain.Course
		var hours []byte
		err := row.Scan(&c.Code, &c.Name, &hours, &c.Topics, &c.Bank)
		if err == nil {
			err = json.Unmarshal(hours, &c.Hours)
		}
		return c, err
	})
}

const questionColumns = `q.id, q.course_code, q.topic, q.stem, q.options, q.answer, q.difficulty, q.explanation, q.author_name, coalesce(q.created_by::text, ''), q.created_at,
	(SELECT count(DISTINCT mq.mock_exam_id) FROM exams.mock_questions mq WHERE mq.question_id = q.id)`

func scanQuestion(row pgx.Row, extra ...any) (domain.Question, error) {
	var q domain.Question
	err := row.Scan(append([]any{&q.ID, &q.CourseCode, &q.Topic, &q.Stem, &q.Options, &q.Answer, &q.Difficulty, &q.Explanation, &q.AuthorName, &q.CreatedBy, &q.CreatedAt, &q.Uses}, extra...)...)
	return q, err
}

func (r *ExamsRepository) Questions(ctx context.Context, f domain.QuestionFilter) ([]domain.Question, int, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+questionColumns+`, count(*) OVER () FROM exams.questions q
		WHERE q.institution_id = $1 AND ($2 = '' OR q.course_code = $2) AND ($3 = '' OR q.topic = $3) AND ($4 = '' OR q.difficulty = $4)
			AND ($5 = '' OR q.stem ILIKE '%' || $5 || '%')
		ORDER BY q.course_code, q.topic, q.created_at DESC LIMIT $6 OFFSET $7`, f.InstitutionID, f.CourseCode, f.Topic, f.Difficulty, f.Query, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, examsErr(err)
	}
	defer rows.Close()
	var out []domain.Question
	total := 0
	for rows.Next() {
		q, err := scanQuestion(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, q)
	}
	return out, total, examsErr(rows.Err())
}

func (r *ExamsRepository) Question(ctx context.Context, institutionID, id string) (domain.Question, error) {
	q, err := scanQuestion(r.c.pool.QueryRow(ctx, `SELECT `+questionColumns+` FROM exams.questions q WHERE q.institution_id = $1 AND q.id = $2`, institutionID, id))
	return q, examsErr(err)
}

func (r *ExamsRepository) SaveQuestion(ctx context.Context, institutionID string, q domain.Question) (domain.Question, error) {
	var id string
	var err error
	if q.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO exams.questions (institution_id, course_code, topic, stem, options, answer, difficulty, explanation, created_by, author_name)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, '')::uuid, $10) RETURNING id`,
			institutionID, q.CourseCode, q.Topic, q.Stem, q.Options, q.Answer, q.Difficulty, q.Explanation, q.CreatedBy, q.AuthorName).Scan(&id)
	} else {
		id = q.ID
		var tag pgconn.CommandTag
		tag, err = r.c.pool.Exec(ctx, `UPDATE exams.questions SET course_code = $3, topic = $4, stem = $5, options = $6, answer = $7, difficulty = $8, explanation = $9, updated_at = now()
			WHERE institution_id = $1 AND id = $2`, institutionID, q.ID, q.CourseCode, q.Topic, q.Stem, q.Options, q.Answer, q.Difficulty, q.Explanation)
		if err == nil && tag.RowsAffected() == 0 {
			return domain.Question{}, domain.ErrNotFound
		}
		if err == nil {
			// Keys built from this question follow its answer
			_, err = r.c.pool.Exec(ctx, `UPDATE exams.mock_questions SET answer = $2 WHERE question_id = $1`, q.ID, q.Answer)
		}
	}
	if err != nil {
		return domain.Question{}, examsErr(err)
	}
	return r.Question(ctx, institutionID, id)
}

func (r *ExamsRepository) DeleteQuestion(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM exams.questions WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return examsErr(err)
}

func (r *ExamsRepository) PickQuestions(ctx context.Context, institutionID, courseCode string, n int, exclude []string) ([]domain.Question, error) {
	if exclude == nil {
		exclude = []string{}
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+questionColumns+` FROM exams.questions q
		WHERE q.institution_id = $1 AND q.course_code = $2 AND NOT (q.id = ANY($4::uuid[])) ORDER BY random() LIMIT $3`, institutionID, courseCode, n, exclude)
	if err != nil {
		return nil, examsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Question, error) { return scanQuestion(row) })
}

func (r *ExamsRepository) QuestionsByID(ctx context.Context, institutionID string, ids []string) ([]domain.Question, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+questionColumns+` FROM exams.questions q WHERE q.institution_id = $1 AND q.id = ANY($2::uuid[])`, institutionID, ids)
	if err != nil {
		return nil, examsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Question, error) { return scanQuestion(row) })
}

const mockColumns = `m.id, m.period_id, p.name, m.name, m.held_on, m.duration_minutes, m.points_right::float8, m.points_wrong::float8, m.status, m.notes,
	(SELECT count(*) FROM exams.mock_questions mq WHERE mq.mock_exam_id = m.id),
	(SELECT count(*) FROM exams.mock_questions mq WHERE mq.mock_exam_id = m.id AND mq.answer <> ''),
	(SELECT count(*) FROM exams.answer_sheets a WHERE a.mock_exam_id = m.id)`

func scanMock(row pgx.Row) (domain.MockExam, error) {
	var m domain.MockExam
	err := row.Scan(&m.ID, &m.PeriodID, &m.PeriodName, &m.Name, &m.HeldOn, &m.Duration, &m.PointsRight, &m.PointsWrong, &m.Status, &m.Notes, &m.Questions, &m.Keyed, &m.Sheets)
	return m, err
}

func (r *ExamsRepository) MockExams(ctx context.Context, institutionID, periodID string) ([]domain.MockExam, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+mockColumns+` FROM exams.mock_exams m JOIN academic.periods p ON p.id = m.period_id
		WHERE m.institution_id = $1 AND ($2 = '' OR m.period_id = nullif($2, '')::uuid) ORDER BY m.held_on DESC, m.created_at DESC`, institutionID, periodID)
	if err != nil {
		return nil, examsErr(err)
	}
	mocks, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MockExam, error) { return scanMock(row) })
	if err != nil {
		return nil, err
	}
	for i := range mocks {
		if err := r.structure(ctx, &mocks[i]); err != nil {
			return nil, err
		}
	}
	return mocks, nil
}

func (r *ExamsRepository) structure(ctx context.Context, m *domain.MockExam) error {
	rows, err := r.c.pool.Query(ctx, `SELECT course_code, course_name, questions, position FROM exams.mock_courses WHERE mock_exam_id = $1 ORDER BY position`, m.ID)
	if err != nil {
		return examsErr(err)
	}
	if m.Courses, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MockCourse, error) {
		var c domain.MockCourse
		err := row.Scan(&c.Code, &c.Name, &c.Questions, &c.Position)
		return c, err
	}); err != nil {
		return err
	}
	rows, err = r.c.pool.Query(ctx, `SELECT level_id, course_code, weight::float8 FROM exams.mock_weights WHERE mock_exam_id = $1`, m.ID)
	if err != nil {
		return examsErr(err)
	}
	m.Weights, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MockWeight, error) {
		var w domain.MockWeight
		err := row.Scan(&w.LevelID, &w.CourseCode, &w.Weight)
		return w, err
	})
	return err
}

func (r *ExamsRepository) MockExam(ctx context.Context, institutionID, id string) (domain.MockExam, error) {
	m, err := scanMock(r.c.pool.QueryRow(ctx, `SELECT `+mockColumns+` FROM exams.mock_exams m JOIN academic.periods p ON p.id = m.period_id WHERE m.institution_id = $1 AND m.id = $2`, institutionID, id))
	if err != nil {
		return m, examsErr(err)
	}
	return m, r.structure(ctx, &m)
}

func (r *ExamsRepository) SaveMockExam(ctx context.Context, institutionID string, m domain.MockExam) (domain.MockExam, error) {
	var id string
	var err error
	if m.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO exams.mock_exams (institution_id, period_id, name, held_on, duration_minutes, points_right, points_wrong, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, institutionID, m.PeriodID, m.Name, date(m.HeldOn), m.Duration, m.PointsRight, m.PointsWrong, m.Notes).Scan(&id)
	} else {
		id = m.ID
		_, err = r.c.pool.Exec(ctx, `UPDATE exams.mock_exams SET name = $3, held_on = $4, duration_minutes = $5, points_right = $6, points_wrong = $7, notes = $8, updated_at = now()
			WHERE institution_id = $1 AND id = $2`, institutionID, m.ID, m.Name, date(m.HeldOn), m.Duration, m.PointsRight, m.PointsWrong, m.Notes)
	}
	if err != nil {
		return domain.MockExam{}, examsErr(err)
	}
	return r.MockExam(ctx, institutionID, id)
}

func (r *ExamsRepository) DeleteMockExam(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM exams.mock_exams WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return examsErr(err)
}

func (r *ExamsRepository) SetStatus(ctx context.Context, institutionID, id, status string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE exams.mock_exams SET status = $3, updated_at = now() WHERE institution_id = $1 AND id = $2`, institutionID, id, status)
	return examsErr(err)
}

func (r *ExamsRepository) SetStructure(ctx context.Context, mockID string, courses []domain.MockCourse, weights []domain.MockWeight) error {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	old, err := tx.Query(ctx, `SELECT number, course_code, coalesce(question_id::text, ''), answer FROM exams.mock_questions WHERE mock_exam_id = $1 ORDER BY number`, mockID)
	if err != nil {
		return examsErr(err)
	}
	byCourse := map[string][]domain.KeyItem{}
	for old.Next() {
		var k domain.KeyItem
		if err := old.Scan(&k.Number, &k.CourseCode, &k.QuestionID, &k.Answer); err != nil {
			old.Close()
			return err
		}
		byCourse[k.CourseCode] = append(byCourse[k.CourseCode], k)
	}
	old.Close()
	for _, q := range []string{`DELETE FROM exams.mock_courses WHERE mock_exam_id = $1`, `DELETE FROM exams.mock_weights WHERE mock_exam_id = $1`, `DELETE FROM exams.mock_questions WHERE mock_exam_id = $1`} {
		if _, err := tx.Exec(ctx, q, mockID); err != nil {
			return examsErr(err)
		}
	}
	batch := &pgx.Batch{}
	number := 0
	for i, c := range courses {
		batch.Queue(`INSERT INTO exams.mock_courses (mock_exam_id, course_code, course_name, questions, position) VALUES ($1, $2, $3, $4, $5)`, mockID, c.Code, c.Name, c.Questions, i)
		// The course keeps its questions (in order) as far as they fit
		prev := byCourse[c.Code]
		for j := 0; j < c.Questions; j++ {
			number++
			var qid, answer string
			if j < len(prev) {
				qid, answer = prev[j].QuestionID, prev[j].Answer
			}
			batch.Queue(`INSERT INTO exams.mock_questions (mock_exam_id, number, course_code, question_id, answer) VALUES ($1, $2, $3, nullif($4, '')::uuid, $5)`, mockID, number, c.Code, qid, answer)
		}
	}
	for _, w := range weights {
		batch.Queue(`INSERT INTO exams.mock_weights (mock_exam_id, level_id, course_code, weight) VALUES ($1, $2, $3, $4)`, mockID, w.LevelID, w.CourseCode, w.Weight)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return examsErr(err)
	}
	return tx.Commit(ctx)
}

func (r *ExamsRepository) Key(ctx context.Context, mockID string) ([]domain.KeyItem, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT number, course_code, coalesce(question_id::text, ''), answer FROM exams.mock_questions WHERE mock_exam_id = $1 ORDER BY number`, mockID)
	if err != nil {
		return nil, examsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.KeyItem, error) {
		var k domain.KeyItem
		err := row.Scan(&k.Number, &k.CourseCode, &k.QuestionID, &k.Answer)
		return k, err
	})
}

func (r *ExamsRepository) SaveKey(ctx context.Context, mockID string, items []domain.KeyItem) error {
	batch := &pgx.Batch{}
	for _, k := range items {
		batch.Queue(`UPDATE exams.mock_questions SET question_id = nullif($3, '')::uuid, answer = $4 WHERE mock_exam_id = $1 AND number = $2`, mockID, k.Number, k.QuestionID, k.Answer)
	}
	return examsErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *ExamsRepository) PeriodStudents(ctx context.Context, institutionID, periodID string) ([]domain.ExamStudent, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, s.code, s.document_number, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			coalesce(sec.id::text, ''), coalesce(sec.name, ''), l.id, l.name
		FROM enrollment.enrollments e JOIN people.students s ON s.id = e.student_id JOIN academic.levels l ON l.id = e.level_id
		LEFT JOIN academic.sections sec ON sec.id = e.section_id
		WHERE e.institution_id = $1 AND e.period_id = $2 AND e.status IN ('active', 'completed')
		ORDER BY l.position, sec.name, s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, periodID)
	if err != nil {
		return nil, examsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ExamStudent, error) {
		var s domain.ExamStudent
		err := row.Scan(&s.ID, &s.Code, &s.Document, &s.Name, &s.SectionID, &s.SectionLabel, &s.LevelID, &s.LevelName)
		return s, err
	})
}

const sheetColumns = `a.id, a.mock_exam_id, a.student_id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
	coalesce(a.section_id::text, ''), coalesce(sec.name, ''), coalesce(a.level_id::text, ''), coalesce(l.name, ''),
	a.answers, a.source, a.score::float8, a.right_count, a.wrong_count, a.blank_count, a.by_course`

const sheetFrom = ` FROM exams.answer_sheets a JOIN people.students s ON s.id = a.student_id
	LEFT JOIN academic.sections sec ON sec.id = a.section_id LEFT JOIN academic.levels l ON l.id = a.level_id`

func scanSheet(row pgx.Row) (domain.AnswerSheet, error) {
	var a domain.AnswerSheet
	var by []byte
	err := row.Scan(&a.ID, &a.MockExamID, &a.StudentID, &a.StudentCode, &a.StudentName, &a.SectionID, &a.SectionLabel, &a.LevelID, &a.LevelName,
		&a.Answers, &a.Source, &a.Score, &a.Right, &a.Wrong, &a.Blank, &by)
	if err == nil {
		err = json.Unmarshal(by, &a.ByCourse)
	}
	return a, err
}

func (r *ExamsRepository) Sheets(ctx context.Context, mockID string) ([]domain.AnswerSheet, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+sheetColumns+sheetFrom+` WHERE a.mock_exam_id = $1 ORDER BY a.score DESC`, mockID)
	if err != nil {
		return nil, examsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AnswerSheet, error) { return scanSheet(row) })
}

func (r *ExamsRepository) SaveSheets(ctx context.Context, mockID string, sheets []domain.AnswerSheet) error {
	batch := &pgx.Batch{}
	for _, a := range sheets {
		by, err := json.Marshal(a.ByCourse)
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO exams.answer_sheets (mock_exam_id, student_id, section_id, level_id, answers, source, score, right_count, wrong_count, blank_count, by_course)
			VALUES ($1, $2, nullif($3, '')::uuid, nullif($4, '')::uuid, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (mock_exam_id, student_id) DO UPDATE SET section_id = EXCLUDED.section_id, level_id = EXCLUDED.level_id, answers = EXCLUDED.answers,
				source = EXCLUDED.source, score = EXCLUDED.score, right_count = EXCLUDED.right_count, wrong_count = EXCLUDED.wrong_count,
				blank_count = EXCLUDED.blank_count, by_course = EXCLUDED.by_course, updated_at = now()`,
			mockID, a.StudentID, a.SectionID, a.LevelID, a.Answers, a.Source, a.Score, a.Right, a.Wrong, a.Blank, by)
	}
	return examsErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *ExamsRepository) DeleteSheet(ctx context.Context, mockID, studentID string) error {
	_, err := r.c.pool.Exec(ctx, `DELETE FROM exams.answer_sheets WHERE mock_exam_id = $1 AND student_id = $2`, mockID, studentID)
	return examsErr(err)
}

func (r *ExamsRepository) StudentSheets(ctx context.Context, institutionID, studentID string, all bool) ([]domain.AnswerSheet, []domain.MockExam, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT m.id FROM exams.answer_sheets a JOIN exams.mock_exams m ON m.id = a.mock_exam_id
		WHERE m.institution_id = $1 AND a.student_id = $2 AND ($3 OR m.status = 'published') ORDER BY m.held_on, m.created_at`, institutionID, studentID, all)
	if err != nil {
		return nil, nil, examsErr(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, nil, err
	}
	var sheets []domain.AnswerSheet
	var mocks []domain.MockExam
	for _, id := range ids {
		m, err := r.MockExam(ctx, institutionID, id)
		if err != nil {
			return nil, nil, err
		}
		all, err := r.Sheets(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		mocks = append(mocks, m)
		sheets = append(sheets, all...)
	}
	return sheets, mocks, nil
}

func (r *ExamsRepository) Admissions(ctx context.Context, institutionID string) ([]domain.Admission, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT a.id, a.student_id, s.given_names || ' ' || s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), ''), s.code,
			a.university, a.career, a.exam, a.admitted_on, a.place, a.score::float8
		FROM exams.admissions a JOIN people.students s ON s.id = a.student_id WHERE a.institution_id = $1 ORDER BY a.admitted_on DESC, a.place NULLS LAST, s.paternal_surname`, institutionID)
	if err != nil {
		return nil, examsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Admission, error) {
		var a domain.Admission
		var place *int32
		err := row.Scan(&a.ID, &a.StudentID, &a.StudentName, &a.StudentCode, &a.University, &a.Career, &a.Exam, &a.AdmittedOn, &place, &a.Score)
		if place != nil {
			v := int(*place)
			a.Place = &v
		}
		return a, err
	})
}

func (r *ExamsRepository) SaveAdmission(ctx context.Context, institutionID string, a domain.Admission) (domain.Admission, error) {
	var id string
	var err error
	if a.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO exams.admissions (institution_id, student_id, university, career, exam, admitted_on, place, score)
			SELECT $1, s.id, $3, $4, $5, $6, $7, $8 FROM people.students s WHERE s.institution_id = $1 AND s.id = $2 RETURNING id`,
			institutionID, a.StudentID, a.University, a.Career, a.Exam, date(a.AdmittedOn), a.Place, a.Score).Scan(&id)
	} else {
		id = a.ID
		var tag pgconn.CommandTag
		tag, err = r.c.pool.Exec(ctx, `UPDATE exams.admissions SET university = $3, career = $4, exam = $5, admitted_on = $6, place = $7, score = $8 WHERE institution_id = $1 AND id = $2`,
			institutionID, a.ID, a.University, a.Career, a.Exam, date(a.AdmittedOn), a.Place, a.Score)
		if err == nil && tag.RowsAffected() == 0 {
			err = pgx.ErrNoRows
		}
	}
	if err != nil {
		return domain.Admission{}, examsErr(err)
	}
	list, err := r.Admissions(ctx, institutionID)
	if err != nil {
		return domain.Admission{}, err
	}
	for _, x := range list {
		if x.ID == id {
			return x, nil
		}
	}
	return domain.Admission{}, domain.ErrNotFound
}

func (r *ExamsRepository) DeleteAdmission(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM exams.admissions WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return examsErr(err)
}

// ---------- demo seed ----------

func (r *ExamsRepository) HasBank(ctx context.Context, institutionID string) (bool, error) {
	var any bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM exams.questions WHERE institution_id = $1)`, institutionID).Scan(&any)
	return any, examsErr(err)
}

// SeedAlumni stores former students (given names, paternal and maternal surname) as graduated, with demo DNIs out
// of the real range
func (r *ExamsRepository) SeedAlumni(ctx context.Context, institutionID string, people [][3]string) ([]string, error) {
	ids := make([]string, 0, len(people))
	for i, p := range people {
		var id string
		err := r.c.pool.QueryRow(ctx, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname, maternal_surname, status)
			VALUES ($1, $2, 'dni', $3, $4, $5, $6, 'graduated') RETURNING id`,
			institutionID, fmt.Sprintf("EGR-%03d", i+1), fmt.Sprintf("799%05d", 100+i), p[0], p[1], p[2]).Scan(&id)
		if err != nil {
			return nil, examsErr(err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
