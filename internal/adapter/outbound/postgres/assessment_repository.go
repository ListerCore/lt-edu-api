package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// AssessmentRepository implements port.AssessmentRepository
type AssessmentRepository struct{ c *Client }

func NewAssessmentRepository(c *Client) *AssessmentRepository { return &AssessmentRepository{c: c} }

var _ port.AssessmentRepository = (*AssessmentRepository)(nil)

// sameTerm compares a term_id column with an optional id parameter ("" = the whole period)
func sameTerm(column, param string) string {
	return column + ` IS NOT DISTINCT FROM nullif(` + param + `, '')::uuid`
}

// unitFilter keeps the students of enrollment e who take subject $param (IEST: only their units)
func unitFilter(param string) string {
	return `(` + param + ` = '' OR NOT EXISTS (SELECT 1 FROM enrollment.unit_enrollments u WHERE u.enrollment_id = e.id)
		OR EXISTS (SELECT 1 FROM enrollment.unit_enrollments u WHERE u.enrollment_id = e.id AND u.subject_id::text = ` + param + `))`
}

func (r *AssessmentRepository) Section(ctx context.Context, institutionID, sectionID string) (domain.SectionMeta, error) {
	var s domain.SectionMeta
	err := r.c.pool.QueryRow(ctx, `SELECT sec.id, coalesce(g.short_name || ' ', '') || sec.name, l.name, sec.period_id, coalesce(sec.tutor_id::text, ''),
			coalesce(t.given_names || ' ' || t.surnames, ''), coalesce(g.name, ''), sec.name
		FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id LEFT JOIN people.staff t ON t.id = sec.tutor_id
		WHERE p.institution_id = $1 AND sec.id = $2`, institutionID, sectionID).Scan(&s.ID, &s.Label, &s.LevelName, &s.PeriodID, &s.TutorID, &s.TutorName, &s.GradeName, &s.SectionName)
	return s, academicErr(err)
}

func (r *AssessmentRepository) Students(ctx context.Context, institutionID, sectionID, subjectID string) ([]domain.StudentRef, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, s.document_number
		FROM enrollment.enrollments e JOIN people.students s ON s.id = e.student_id
		WHERE e.institution_id = $1 AND e.section_id = $2 AND e.status = 'active' AND `+unitFilter("$3")+`
		  -- Students exempted from Educación Religiosa leave its register
		  AND NOT EXISTS (SELECT 1 FROM enrollment.religion_exemptions x JOIN academic.subjects sj ON sj.code = 'educacion-religiosa' AND sj.id::text = $3
			WHERE x.enrollment_id = e.id)
		ORDER BY s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, sectionID, subjectID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.StudentRef, error) {
		var s domain.StudentRef
		err := row.Scan(&s.ID, &s.Code, &s.Name, &s.Document)
		return s, err
	})
}

func scanCompetencies(rows pgx.Rows, err error) ([]domain.CompetencyRef, error) {
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.CompetencyRef, error) {
		var c domain.CompetencyRef
		err := row.Scan(&c.ID, &c.SubjectID, &c.Code, &c.Name, &c.Transversal)
		return c, err
	})
}

func (r *AssessmentRepository) Competencies(ctx context.Context, institutionID, subjectID string) ([]domain.CompetencyRef, error) {
	return scanCompetencies(r.c.pool.Query(ctx, `SELECT c.id, coalesce(c.subject_id::text, ''), c.code, c.name, false FROM academic.competencies c
		JOIN academic.curricula cu ON cu.id = c.curriculum_id
		WHERE cu.institution_id = $1 AND c.subject_id = $2 AND c.kind = 'competency' ORDER BY c.position`, institutionID, subjectID))
}

func (r *AssessmentRepository) Transversal(ctx context.Context, institutionID, sectionID string) ([]domain.CompetencyRef, error) {
	return scanCompetencies(r.c.pool.Query(ctx, `SELECT c.id, '', c.code, c.name, true FROM academic.competencies c
		JOIN academic.curricula cu ON cu.id = c.curriculum_id
		WHERE cu.institution_id = $1 AND c.subject_id IS NULL AND c.kind = 'competency'
			AND c.curriculum_id IN (SELECT sub.curriculum_id FROM academic.assignments a JOIN academic.subjects sub ON sub.id = a.subject_id WHERE a.section_id = $2)
		ORDER BY c.position`, institutionID, sectionID))
}

func (r *AssessmentRepository) CompetencyMarks(ctx context.Context, institutionID, sectionID, termID string, all bool) ([]domain.CompetencyMark, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT student_id, competency_id, coalesce(term_id::text, ''), level, conclusion FROM assessment.competency_marks
		WHERE institution_id = $1 AND section_id = $2 AND ($4 OR `+sameTerm("term_id", "$3")+`)`, institutionID, sectionID, termID, all)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.CompetencyMark, error) {
		var m domain.CompetencyMark
		err := row.Scan(&m.StudentID, &m.CompetencyID, &m.TermID, &m.Level, &m.Conclusion)
		return m, err
	})
}

func (r *AssessmentRepository) SaveCompetencyMarks(ctx context.Context, institutionID, sectionID, termID string, marks []domain.CompetencyMark, userID string) error {
	batch := &pgx.Batch{}
	for _, m := range marks {
		if m.Level == "" && m.Conclusion == "" {
			batch.Queue(`DELETE FROM assessment.competency_marks WHERE section_id = $1 AND `+sameTerm("term_id", "$2")+` AND student_id = $3 AND competency_id = $4`,
				sectionID, termID, m.StudentID, m.CompetencyID)
			continue
		}
		batch.Queue(`INSERT INTO assessment.competency_marks (institution_id, section_id, term_id, student_id, competency_id, level, conclusion, updated_by)
			VALUES ($1, $2, nullif($3, '')::uuid, $4, $5, $6, $7, nullif($8, '')::uuid)
			ON CONFLICT (section_id, term_id, student_id, competency_id) DO UPDATE SET level = EXCLUDED.level, conclusion = EXCLUDED.conclusion,
				updated_by = EXCLUDED.updated_by, updated_at = now()`, institutionID, sectionID, termID, m.StudentID, m.CompetencyID, m.Level, m.Conclusion, userID)
	}
	return academicErr(r.c.pool.SendBatch(ctx, batch).Close())
}

const evaluationColumns = `id, assignment_id, coalesce(term_id::text, ''), name, weight, held_on, position`

func scanEvaluation(row pgx.Row) (domain.Evaluation, error) {
	var e domain.Evaluation
	err := row.Scan(&e.ID, &e.AssignmentID, &e.TermID, &e.Name, &e.Weight, &e.HeldOn, &e.Position)
	return e, err
}

func (r *AssessmentRepository) Evaluations(ctx context.Context, institutionID, assignmentID, termID string) ([]domain.Evaluation, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+evaluationColumns+` FROM assessment.evaluations
		WHERE institution_id = $1 AND assignment_id = $2 AND `+sameTerm("term_id", "$3")+` ORDER BY position, created_at`, institutionID, assignmentID, termID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Evaluation, error) { return scanEvaluation(row) })
}

func (r *AssessmentRepository) SectionEvaluations(ctx context.Context, institutionID, sectionID string) ([]domain.Evaluation, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+evaluationColumns+` FROM assessment.evaluations
		WHERE institution_id = $1 AND assignment_id IN (SELECT id FROM academic.assignments WHERE section_id = $2) ORDER BY position, created_at`, institutionID, sectionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Evaluation, error) { return scanEvaluation(row) })
}

func (r *AssessmentRepository) Evaluation(ctx context.Context, institutionID, id string) (domain.Evaluation, error) {
	e, err := scanEvaluation(r.c.pool.QueryRow(ctx, `SELECT `+evaluationColumns+` FROM assessment.evaluations WHERE institution_id = $1 AND id = $2`, institutionID, id))
	return e, academicErr(err)
}

func (r *AssessmentRepository) SaveEvaluation(ctx context.Context, institutionID string, e domain.Evaluation) (domain.Evaluation, error) {
	var held *time.Time
	if e.HeldOn != nil {
		d := date(*e.HeldOn)
		held = &d
	}
	var row pgx.Row
	if e.ID == "" {
		row = r.c.pool.QueryRow(ctx, `INSERT INTO assessment.evaluations (institution_id, assignment_id, term_id, name, weight, held_on, position)
			VALUES ($1, $2, nullif($3, '')::uuid, $4, $5, $6,
				(SELECT coalesce(max(position) + 1, 0) FROM assessment.evaluations WHERE assignment_id = $2 AND `+sameTerm("term_id", "$3")+`))
			RETURNING `+evaluationColumns, institutionID, e.AssignmentID, e.TermID, e.Name, e.Weight, held)
	} else {
		row = r.c.pool.QueryRow(ctx, `UPDATE assessment.evaluations SET name = $3, weight = $4, held_on = $5 WHERE institution_id = $1 AND id = $2
			RETURNING `+evaluationColumns, institutionID, e.ID, e.Name, e.Weight, held)
	}
	out, err := scanEvaluation(row)
	return out, academicErr(err)
}

func (r *AssessmentRepository) DeleteEvaluation(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM assessment.evaluations WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *AssessmentRepository) Scores(ctx context.Context, evaluationIDs []string) ([]domain.Score, error) {
	if len(evaluationIDs) == 0 {
		return nil, nil
	}
	rows, err := r.c.pool.Query(ctx, `SELECT evaluation_id, student_id, score FROM assessment.scores WHERE evaluation_id = ANY($1::uuid[])`, evaluationIDs)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Score, error) {
		var s domain.Score
		err := row.Scan(&s.EvaluationID, &s.StudentID, &s.Score)
		return s, err
	})
}

func (r *AssessmentRepository) SaveScores(ctx context.Context, evaluationID string, scores map[string]*int) error {
	batch := &pgx.Batch{}
	for student, score := range scores {
		if score == nil {
			batch.Queue(`DELETE FROM assessment.scores WHERE evaluation_id = $1 AND student_id = $2`, evaluationID, student)
			continue
		}
		batch.Queue(`INSERT INTO assessment.scores (evaluation_id, student_id, score) VALUES ($1, $2, $3)
			ON CONFLICT (evaluation_id, student_id) DO UPDATE SET score = EXCLUDED.score, updated_at = now()`, evaluationID, student, *score)
	}
	return academicErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *AssessmentRepository) Conduct(ctx context.Context, institutionID, sectionID, termID string, all bool) ([]domain.ConductMark, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT student_id, coalesce(term_id::text, ''), score, note FROM assessment.conduct_marks
		WHERE institution_id = $1 AND section_id = $2 AND ($4 OR `+sameTerm("term_id", "$3")+`)`, institutionID, sectionID, termID, all)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ConductMark, error) {
		var m domain.ConductMark
		err := row.Scan(&m.StudentID, &m.TermID, &m.Score, &m.Note)
		return m, err
	})
}

func (r *AssessmentRepository) SaveConduct(ctx context.Context, institutionID, sectionID, termID string, scores map[string]*int, notes map[string]string) error {
	batch := &pgx.Batch{}
	for student, score := range scores {
		if score == nil {
			batch.Queue(`DELETE FROM assessment.conduct_marks WHERE section_id = $1 AND `+sameTerm("term_id", "$2")+` AND student_id = $3`, sectionID, termID, student)
			continue
		}
		batch.Queue(`INSERT INTO assessment.conduct_marks (institution_id, section_id, term_id, student_id, score, note) VALUES ($1, $2, nullif($3, '')::uuid, $4, $5, $6)
			ON CONFLICT (section_id, term_id, student_id) DO UPDATE SET score = EXCLUDED.score, note = EXCLUDED.note, updated_at = now()`,
			institutionID, sectionID, termID, student, *score, notes[student])
	}
	return academicErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *AssessmentRepository) Comments(ctx context.Context, sectionID, termID string) ([]domain.TutorComment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT student_id, coalesce(term_id::text, ''), comment FROM assessment.tutor_comments WHERE section_id = $1 AND `+sameTerm("term_id", "$2"), sectionID, termID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TutorComment, error) {
		var c domain.TutorComment
		err := row.Scan(&c.StudentID, &c.TermID, &c.Comment)
		return c, err
	})
}

func (r *AssessmentRepository) SaveComments(ctx context.Context, sectionID, termID string, comments map[string]string) error {
	batch := &pgx.Batch{}
	for student, text := range comments {
		if text == "" {
			batch.Queue(`DELETE FROM assessment.tutor_comments WHERE section_id = $1 AND `+sameTerm("term_id", "$2")+` AND student_id = $3`, sectionID, termID, student)
			continue
		}
		batch.Queue(`INSERT INTO assessment.tutor_comments (section_id, term_id, student_id, comment) VALUES ($1, nullif($2, '')::uuid, $3, $4)
			ON CONFLICT (section_id, term_id, student_id) DO UPDATE SET comment = EXCLUDED.comment, updated_at = now()`, sectionID, termID, student, text)
	}
	return academicErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *AssessmentRepository) Submissions(ctx context.Context, institutionID, periodID, termID string) ([]domain.Submission, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT su.section_id, coalesce(su.assignment_id::text, ''), coalesce(su.term_id::text, ''), su.submitted_name, su.submitted_at
		FROM assessment.submissions su JOIN academic.sections sec ON sec.id = su.section_id
		WHERE su.institution_id = $1 AND sec.period_id = $2 AND `+sameTerm("su.term_id", "$3"), institutionID, periodID, termID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Submission, error) {
		var s domain.Submission
		err := row.Scan(&s.SectionID, &s.AssignmentID, &s.TermID, &s.SubmittedName, &s.SubmittedAt)
		return s, err
	})
}

func (r *AssessmentRepository) SetSubmission(ctx context.Context, institutionID, sectionID, assignmentID, termID string, sent bool, userID, name string) error {
	var err error
	if sent {
		_, err = r.c.pool.Exec(ctx, `INSERT INTO assessment.submissions (institution_id, section_id, assignment_id, term_id, submitted_by, submitted_name)
			VALUES ($1, $2, nullif($3, '')::uuid, nullif($4, '')::uuid, nullif($5, '')::uuid, $6)
			ON CONFLICT (section_id, assignment_id, term_id) DO UPDATE SET submitted_by = EXCLUDED.submitted_by, submitted_name = EXCLUDED.submitted_name, submitted_at = now()`,
			institutionID, sectionID, assignmentID, termID, userID, name)
	} else {
		_, err = r.c.pool.Exec(ctx, `DELETE FROM assessment.submissions WHERE institution_id = $1 AND section_id = $2 AND `+sameTerm("assignment_id", "$3")+` AND `+sameTerm("term_id", "$4"),
			institutionID, sectionID, assignmentID, termID)
	}
	return academicErr(err)
}

func (r *AssessmentRepository) Progress(ctx context.Context, institutionID, periodID, termID string, numeric bool, staffID string) ([]domain.SheetProgress, error) {
	students := `(SELECT count(*) FROM enrollment.enrollments e WHERE e.section_id = a.section_id AND e.status = 'active' AND ` + unitFilter("a.subject_id::text") + `)`
	var cells string
	if numeric {
		cells = `(SELECT count(*) FROM assessment.evaluations ev WHERE ev.assignment_id = a.id AND ` + sameTerm("ev.term_id", "$3") + `),
			(SELECT count(*) FROM assessment.scores sc JOIN assessment.evaluations ev ON ev.id = sc.evaluation_id WHERE ev.assignment_id = a.id AND ` + sameTerm("ev.term_id", "$3") + `)`
	} else {
		cells = `(SELECT count(*) FROM academic.competencies c WHERE c.subject_id = a.subject_id AND c.kind = 'competency'),
			(SELECT count(*) FROM assessment.competency_marks cm JOIN academic.competencies c ON c.id = cm.competency_id
				WHERE cm.section_id = a.section_id AND c.subject_id = a.subject_id AND cm.level <> '' AND ` + sameTerm("cm.term_id", "$3") + `
					AND EXISTS (SELECT 1 FROM enrollment.enrollments e WHERE e.student_id = cm.student_id AND e.section_id = a.section_id AND e.status = 'active'))`
	}
	rows, err := r.c.pool.Query(ctx, `SELECT a.section_id, coalesce(g.short_name || ' ', '') || sec.name || ' · ' || l.name, a.id, a.subject_id, sub.name,
			coalesce(a.staff_id::text, ''), coalesce(st.given_names || ' ' || st.surnames, ''), `+students+`, `+cells+`
		FROM academic.assignments a
		JOIN academic.sections sec ON sec.id = a.section_id
		JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id
		JOIN academic.subjects sub ON sub.id = a.subject_id
		LEFT JOIN people.staff st ON st.id = a.staff_id
		WHERE a.institution_id = $1 AND a.period_id = $2 AND ($4 = '' OR a.staff_id::text = $4)
		ORDER BY l.position, g.position NULLS FIRST, sec.name, sub.position`, institutionID, periodID, termID, staffID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SheetProgress, error) {
		var p domain.SheetProgress
		var perStudent, filled int
		err := row.Scan(&p.SectionID, &p.SectionLabel, &p.AssignmentID, &p.SubjectID, &p.SubjectName, &p.StaffID, &p.StaffName, &p.Students, &perStudent, &filled)
		p.Expected, p.Filled = p.Students*perStudent, filled
		if numeric {
			p.Evaluations = perStudent
		}
		return p, err
	})
}

// TutorProgress replaces, for a list of sections, the five queries per section of Section, Students, Transversal,
// CompetencyMarks and Conduct with one aggregate: same filters, counted in the database
func (r *AssessmentRepository) TutorProgress(ctx context.Context, institutionID, termID string, sectionIDs []string) ([]domain.TutorCounts, error) {
	if len(sectionIDs) == 0 {
		return nil, nil
	}
	rows, err := r.c.pool.Query(ctx, `WITH sec AS (
			SELECT sec.id, coalesce(g.short_name || ' ', '') || sec.name AS label, l.name AS level_name, coalesce(t.given_names || ' ' || t.surnames, '') AS tutor_name
			FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id JOIN academic.levels l ON l.id = sec.level_id
			LEFT JOIN academic.grades g ON g.id = sec.grade_id LEFT JOIN people.staff t ON t.id = sec.tutor_id
			WHERE p.institution_id = $1 AND sec.id = ANY($2::uuid[])
		), enrolled AS (
			SELECT e.section_id, e.student_id FROM enrollment.enrollments e JOIN people.students s ON s.id = e.student_id
			WHERE e.institution_id = $1 AND e.status = 'active' AND e.section_id IN (SELECT id FROM sec)
		), comps AS (
			SELECT DISTINCT a.section_id, c.id AS competency_id
			FROM academic.assignments a JOIN academic.subjects sub ON sub.id = a.subject_id
			JOIN academic.competencies c ON c.curriculum_id = sub.curriculum_id JOIN academic.curricula cu ON cu.id = c.curriculum_id
			WHERE a.section_id IN (SELECT id FROM sec) AND cu.institution_id = $1 AND c.subject_id IS NULL AND c.kind = 'competency'
		)
		SELECT sec.id, sec.label, sec.level_name, sec.tutor_name,
			(SELECT count(*) FROM enrolled en WHERE en.section_id = sec.id),
			(SELECT count(*) FROM comps co WHERE co.section_id = sec.id),
			(SELECT count(*) FROM assessment.competency_marks cm
				WHERE cm.institution_id = $1 AND cm.section_id = sec.id AND `+sameTerm("cm.term_id", "$3")+` AND cm.level <> ''
					AND EXISTS (SELECT 1 FROM comps co WHERE co.section_id = sec.id AND co.competency_id = cm.competency_id)
					AND EXISTS (SELECT 1 FROM enrolled en WHERE en.section_id = sec.id AND en.student_id = cm.student_id)),
			(SELECT count(*) FROM assessment.conduct_marks cd
				WHERE cd.institution_id = $1 AND cd.section_id = sec.id AND `+sameTerm("cd.term_id", "$3")+`
					AND EXISTS (SELECT 1 FROM enrolled en WHERE en.section_id = sec.id AND en.student_id = cd.student_id))
		FROM sec`, institutionID, sectionIDs, termID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TutorCounts, error) {
		var c domain.TutorCounts
		err := row.Scan(&c.SectionID, &c.Label, &c.LevelName, &c.TutorName, &c.Students, &c.Transversal, &c.Filled, &c.ConductFilled)
		return c, err
	})
}

func (r *AssessmentRepository) StudentSection(ctx context.Context, institutionID, studentID, periodID string) (string, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `SELECT section_id FROM enrollment.enrollments WHERE institution_id = $1 AND student_id = $2 AND period_id = $3 AND section_id IS NOT NULL`,
		institutionID, studentID, periodID).Scan(&id)
	return id, academicErr(err)
}

func (r *AssessmentRepository) StudentPeriods(ctx context.Context, institutionID, studentID string) ([]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.period_id FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id
		WHERE e.institution_id = $1 AND e.student_id = $2 AND e.section_id IS NOT NULL AND e.status IN ('active', 'completed')
		ORDER BY (p.status = 'active') DESC, p.starts_on DESC`, institutionID, studentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func (r *AssessmentRepository) AttendanceBetween(ctx context.Context, institutionID, studentID string, from, to time.Time) (domain.Tally, error) {
	var t domain.Tally
	err := r.c.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'present'), count(*) FILTER (WHERE status = 'late'),
			count(*) FILTER (WHERE status = 'absent'), count(*) FILTER (WHERE status = 'justified')
		FROM attendance.daily_marks WHERE institution_id = $1 AND student_id = $2 AND day BETWEEN $3 AND $4`, institutionID, studentID, date(from), date(to)).
		Scan(&t.Present, &t.Late, &t.Absent, &t.Justified)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, nil
	}
	return t, academicErr(err)
}

func (r *AssessmentRepository) SubjectsOfSection(ctx context.Context, institutionID, sectionID string) ([]domain.Assignment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT a.id, a.period_id, a.section_id, a.subject_id, sub.name, sub.kind, sub.code, coalesce(a.staff_id::text, ''),
			coalesce(st.given_names || ' ' || st.surnames, ''), a.weekly_hours
		FROM academic.assignments a JOIN academic.subjects sub ON sub.id = a.subject_id LEFT JOIN people.staff st ON st.id = a.staff_id
		WHERE a.institution_id = $1 AND a.section_id = $2 ORDER BY sub.position, sub.name`, institutionID, sectionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Assignment, error) {
		var a domain.Assignment
		err := row.Scan(&a.ID, &a.PeriodID, &a.SectionID, &a.SubjectID, &a.SubjectName, &a.SubjectKind, &a.SubjectCode, &a.StaffID, &a.StaffName, &a.WeeklyHours)
		return a, err
	})
}

// ---------- demo seed ----------

var _ port.AssessmentSeeder = (*AssessmentRepository)(nil)

func nullable(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func (r *AssessmentRepository) HasAssessment(ctx context.Context, institutionID string) (bool, error) {
	var any bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM assessment.competency_marks WHERE institution_id = $1)
		OR EXISTS (SELECT 1 FROM assessment.evaluations WHERE institution_id = $1)`, institutionID).Scan(&any)
	return any, academicErr(err)
}

func (r *AssessmentRepository) BulkCompetencyMarks(ctx context.Context, institutionID string, marks []port.SeedMark) (int, error) {
	rows := make([][]any, len(marks))
	for i, m := range marks {
		rows[i] = []any{institutionID, m.SectionID, nullable(m.TermID), m.StudentID, m.CompetencyID, m.Level, m.Conclusion}
	}
	n, err := r.c.pool.CopyFrom(ctx, pgx.Identifier{"assessment", "competency_marks"}, []string{"institution_id", "section_id", "term_id", "student_id", "competency_id", "level", "conclusion"}, pgx.CopyFromRows(rows))
	return int(n), academicErr(err)
}

func (r *AssessmentRepository) BulkConduct(ctx context.Context, institutionID string, marks []port.SeedConduct) error {
	rows := make([][]any, len(marks))
	for i, m := range marks {
		rows[i] = []any{institutionID, m.SectionID, nullable(m.TermID), m.StudentID, m.Score}
	}
	_, err := r.c.pool.CopyFrom(ctx, pgx.Identifier{"assessment", "conduct_marks"}, []string{"institution_id", "section_id", "term_id", "student_id", "score"}, pgx.CopyFromRows(rows))
	return academicErr(err)
}

func (r *AssessmentRepository) BulkComments(ctx context.Context, comments []port.SeedComment) error {
	rows := make([][]any, len(comments))
	for i, c := range comments {
		rows[i] = []any{c.SectionID, nullable(c.TermID), c.StudentID, c.Comment}
	}
	_, err := r.c.pool.CopyFrom(ctx, pgx.Identifier{"assessment", "tutor_comments"}, []string{"section_id", "term_id", "student_id", "comment"}, pgx.CopyFromRows(rows))
	return academicErr(err)
}

func (r *AssessmentRepository) BulkScores(ctx context.Context, scores []domain.Score) error {
	rows := make([][]any, len(scores))
	for i, s := range scores {
		rows[i] = []any{s.EvaluationID, s.StudentID, s.Score}
	}
	_, err := r.c.pool.CopyFrom(ctx, pgx.Identifier{"assessment", "scores"}, []string{"evaluation_id", "student_id", "score"}, pgx.CopyFromRows(rows))
	return academicErr(err)
}
