package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ProgramsRepository implements port.ProgramsRepository
type ProgramsRepository struct{ c *Client }

func NewProgramsRepository(c *Client) *ProgramsRepository { return &ProgramsRepository{c: c} }

var _ port.ProgramsRepository = (*ProgramsRepository)(nil)

func programsErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.ConstraintName {
		case "certificates_one_modular", "certificates_one_degree":
			return domain.ErrAlreadyIssued
		case "internships_ruc_check":
			return domain.Validation(map[string]string{"companyRuc": "El RUC tiene 11 dígitos y empieza con 1 o 2."})
		}
	}
	return academicErr(err)
}

func (r *ProgramsRepository) Programs(ctx context.Context, institutionID string) ([]domain.ProgramInfo, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT l.id, l.code, l.name, coalesce(cu.id::text, ''), coalesce(l.coordinator_id::text, ''), coalesce(st.given_names || ' ' || st.surnames, ''),
			(SELECT count(DISTINCT e.student_id) FROM enrollment.enrollments e WHERE e.level_id = l.id AND e.status = 'active')
		FROM academic.levels l
		LEFT JOIN academic.curricula cu ON cu.level_id = l.id AND cu.status = 'active'
		LEFT JOIN people.staff st ON st.id = l.coordinator_id
		WHERE l.institution_id = $1 AND l.is_active ORDER BY l.position`, institutionID)
	if err != nil {
		return nil, programsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ProgramInfo, error) {
		var p domain.ProgramInfo
		err := row.Scan(&p.LevelID, &p.Code, &p.Name, &p.CurriculumID, &p.CoordinatorID, &p.CoordinatorName, &p.Students)
		return p, err
	})
}

func (r *ProgramsRepository) SetCoordinator(ctx context.Context, institutionID, levelID, staffID string) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.levels SET coordinator_id = nullif($3, '')::uuid WHERE institution_id = $1 AND id = $2`, institutionID, levelID, staffID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return programsErr(err)
}

func (r *ProgramsRepository) Structure(ctx context.Context, institutionID, levelID string) ([]domain.ModuleRef, []domain.UnitRef, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT m.id, m.code, m.name, m.position FROM academic.modules m JOIN academic.curricula cu ON cu.id = m.curriculum_id
		WHERE cu.institution_id = $1 AND cu.level_id = $2 AND cu.status = 'active' ORDER BY m.position`, institutionID, levelID)
	if err != nil {
		return nil, nil, programsErr(err)
	}
	modules, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ModuleRef, error) {
		var m domain.ModuleRef
		err := row.Scan(&m.ID, &m.Code, &m.Name, &m.Position)
		return m, err
	})
	if err != nil {
		return nil, nil, err
	}
	rows, err = r.c.pool.Query(ctx, `SELECT s.id, coalesce(s.module_id::text, ''), s.kind, s.code, s.name, coalesce(s.theory_credits, 0), coalesce(s.practice_credits, 0),
			coalesce(min(g.short_name), ''), coalesce(min(g.position), 0)
		FROM academic.subjects s JOIN academic.curricula cu ON cu.id = s.curriculum_id
		LEFT JOIN academic.subject_grades sg ON sg.subject_id = s.id LEFT JOIN academic.grades g ON g.id = sg.grade_id
		WHERE cu.institution_id = $1 AND cu.level_id = $2 AND cu.status = 'active' AND s.kind IN ('unit', 'efsrt')
		GROUP BY s.id ORDER BY coalesce(min(g.position), 0), s.kind DESC, s.position`, institutionID, levelID)
	if err != nil {
		return nil, nil, programsErr(err)
	}
	units, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UnitRef, error) {
		var u domain.UnitRef
		err := row.Scan(&u.SubjectID, &u.ModuleID, &u.Kind, &u.Code, &u.Name, &u.TheoryCredits, &u.PracticeCredits, &u.Semester, &u.SemesterPos)
		return u, err
	})
	return modules, units, err
}

const programStudent = `SELECT s.id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, s.document_number, s.status,
		x.level_id, coalesce(g.short_name, ''), coalesce(g.position, 0)
	FROM people.students s
	JOIN LATERAL (SELECT e.level_id, e.grade_id FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id
		WHERE e.student_id = s.id ORDER BY p.starts_on DESC LIMIT 1) x ON true
	LEFT JOIN academic.grades g ON g.id = x.grade_id`

func scanProgramStudent(row pgx.Row) (domain.ProgramStudent, error) {
	var p domain.ProgramStudent
	err := row.Scan(&p.ID, &p.Code, &p.Name, &p.Document, &p.Status, &p.LevelID, &p.Semester, &p.SemesterPos)
	return p, err
}

func (r *ProgramsRepository) Students(ctx context.Context, institutionID, levelID, query string) ([]domain.ProgramStudent, error) {
	rows, err := r.c.pool.Query(ctx, programStudent+` WHERE s.institution_id = $1 AND x.level_id = $2
		AND ($3 = '' OR `+fmt.Sprintf(fold, `s.given_names || ' ' || s.paternal_surname || ' ' || s.maternal_surname || ' ' || s.code || ' ' || s.document_number`)+` LIKE '%' || `+fmt.Sprintf(fold, `$3`)+` || '%')
		ORDER BY g.position DESC NULLS LAST, s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, levelID, query)
	if err != nil {
		return nil, programsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ProgramStudent, error) { return scanProgramStudent(row) })
}

func (r *ProgramsRepository) Student(ctx context.Context, institutionID, studentID string) (domain.ProgramStudent, error) {
	p, err := scanProgramStudent(r.c.pool.QueryRow(ctx, programStudent+` WHERE s.institution_id = $1 AND s.id = $2`, institutionID, studentID))
	return p, programsErr(err)
}

func (r *ProgramsRepository) Standings(ctx context.Context, institutionID, studentID string) ([]domain.UnitStanding, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT subject_id, status, grade, period_name, source FROM (
			SELECT a.subject_id, CASE WHEN ur.status = 'approved' THEN 'approved' ELSE 'failed' END AS status, ur.final_grade AS grade, p.name AS period_name, 'closing' AS source, p.starts_on
			FROM assessment.unit_results ur JOIN academic.assignments a ON a.id = ur.assignment_id JOIN academic.periods p ON p.id = a.period_id
			WHERE ur.institution_id = $1 AND ur.student_id = $2 AND ur.status IN ('approved', 'failed', 'barred')
			UNION ALL
			SELECT h.subject_id, h.status, h.grade, h.period_name, h.source, '0001-01-01'::date FROM programs.unit_records h WHERE h.institution_id = $1 AND h.student_id = $2
			UNION ALL
			SELECT u.subject_id, 'ongoing', NULL, p.name, 'enrolled', p.starts_on FROM enrollment.unit_enrollments u JOIN enrollment.enrollments e ON e.id = u.enrollment_id
				JOIN academic.periods p ON p.id = e.period_id WHERE e.institution_id = $1 AND e.student_id = $2 AND e.status = 'active' AND p.status <> 'closed'
		) x ORDER BY starts_on`, institutionID, studentID)
	if err != nil {
		return nil, programsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.UnitStanding, error) {
		var u domain.UnitStanding
		var grade *int16
		err := row.Scan(&u.SubjectID, &u.Status, &grade, &u.PeriodName, &u.Source)
		u.Grade = intOf(grade)
		return u, err
	})
}

const internshipColumns = `i.id, i.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, s.code,
	i.subject_id, sub.name, coalesce(m.name, ''), cu.level_id, l.name, i.company, i.company_ruc, i.area, i.supervisor, i.supervisor_contact, i.starts_on, i.ends_on,
	i.required_hours, coalesce((SELECT sum(hours) FROM programs.internship_logs lg WHERE lg.internship_id = i.id), 0)::float8, i.status, i.score, i.evaluation,
	i.evaluated_name, i.evaluated_at`

const internshipFrom = ` FROM programs.internships i JOIN people.students s ON s.id = i.student_id JOIN academic.subjects sub ON sub.id = i.subject_id
	JOIN academic.curricula cu ON cu.id = sub.curriculum_id JOIN academic.levels l ON l.id = cu.level_id LEFT JOIN academic.modules m ON m.id = sub.module_id`

func scanInternship(row pgx.Row) (domain.Internship, error) {
	var x domain.Internship
	var score *int16
	err := row.Scan(&x.ID, &x.StudentID, &x.StudentName, &x.StudentCode, &x.SubjectID, &x.SubjectName, &x.ModuleName, &x.LevelID, &x.ProgramName, &x.Company, &x.CompanyRUC,
		&x.Area, &x.Supervisor, &x.SupervisorContact, &x.StartsOn, &x.EndsOn, &x.RequiredHours, &x.LoggedHours, &x.Status, &score, &x.Evaluation, &x.EvaluatedName, &x.EvaluatedAt)
	x.Score = intOf(score)
	return x, err
}

func (r *ProgramsRepository) Internships(ctx context.Context, f domain.InternshipFilter) ([]domain.Internship, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+internshipColumns+internshipFrom+`
		WHERE i.institution_id = $1 AND (coalesce(cardinality($2::uuid[]), 0) = 0 OR cu.level_id = ANY($2::uuid[])) AND ($3 = '' OR i.student_id = nullif($3, '')::uuid)
			AND ($4 = '' OR i.status = $4)
		ORDER BY (i.status = 'ongoing') DESC, i.starts_on DESC, s.paternal_surname`, f.InstitutionID, f.LevelIDs, f.StudentID, f.Status)
	if err != nil {
		return nil, programsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Internship, error) { return scanInternship(row) })
}

func (r *ProgramsRepository) Internship(ctx context.Context, institutionID, id string) (domain.Internship, error) {
	x, err := scanInternship(r.c.pool.QueryRow(ctx, `SELECT `+internshipColumns+internshipFrom+` WHERE i.institution_id = $1 AND i.id = $2`, institutionID, id))
	return x, programsErr(err)
}

func (r *ProgramsRepository) SaveInternship(ctx context.Context, institutionID string, x domain.Internship) (domain.Internship, error) {
	var id string
	var err error
	if x.ID == "" {
		err = r.c.pool.QueryRow(ctx, `INSERT INTO programs.internships (institution_id, student_id, subject_id, company, company_ruc, area, supervisor, supervisor_contact, starts_on, ends_on, required_hours)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
			institutionID, x.StudentID, x.SubjectID, x.Company, x.CompanyRUC, x.Area, x.Supervisor, x.SupervisorContact, date(x.StartsOn), x.EndsOn, x.RequiredHours).Scan(&id)
	} else {
		id = x.ID
		_, err = r.c.pool.Exec(ctx, `UPDATE programs.internships SET company = $3, company_ruc = $4, area = $5, supervisor = $6, supervisor_contact = $7, starts_on = $8, ends_on = $9
			WHERE institution_id = $1 AND id = $2`, institutionID, x.ID, x.Company, x.CompanyRUC, x.Area, x.Supervisor, x.SupervisorContact, date(x.StartsOn), x.EndsOn)
	}
	if err != nil {
		return domain.Internship{}, programsErr(err)
	}
	return r.Internship(ctx, institutionID, id)
}

func (r *ProgramsRepository) DeleteInternship(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM programs.internships WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return programsErr(err)
}

func (r *ProgramsRepository) Logs(ctx context.Context, internshipID string) ([]domain.InternshipLog, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, day, hours::float8, activity FROM programs.internship_logs WHERE internship_id = $1 ORDER BY day DESC, created_at DESC`, internshipID)
	if err != nil {
		return nil, programsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.InternshipLog, error) {
		var l domain.InternshipLog
		err := row.Scan(&l.ID, &l.Day, &l.Hours, &l.Activity)
		return l, err
	})
}

func (r *ProgramsRepository) AddLog(ctx context.Context, internshipID string, l domain.InternshipLog) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO programs.internship_logs (internship_id, day, hours, activity) VALUES ($1, $2, $3, $4)`, internshipID, date(l.Day), l.Hours, l.Activity)
	return programsErr(err)
}

func (r *ProgramsRepository) DeleteLog(ctx context.Context, internshipID, logID string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM programs.internship_logs WHERE internship_id = $1 AND id = $2`, internshipID, logID)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return programsErr(err)
}

func (r *ProgramsRepository) Evaluate(ctx context.Context, institutionID, id, status string, score int, evaluation, name string, endsOn time.Time) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE programs.internships SET status = $3, score = $4, evaluation = $5, evaluated_name = $6, evaluated_at = now(), ends_on = coalesce(ends_on, $7)
		WHERE institution_id = $1 AND id = $2`, institutionID, id, status, score, evaluation, name, date(endsOn))
	return programsErr(err)
}

const certificateColumns = `c.id, c.student_id, s.given_names || ' ' || s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), ''), s.code, c.level_id, l.name,
	c.kind, coalesce(c.module_id::text, ''), coalesce(m.name, ''), c.number, c.code, c.status, c.issued_on, c.issued_name, c.annul_reason`

const certificateFrom = ` FROM programs.certificates c JOIN people.students s ON s.id = c.student_id JOIN academic.levels l ON l.id = c.level_id
	LEFT JOIN academic.modules m ON m.id = c.module_id`

func scanCertificate(row pgx.Row, withContent bool) (domain.Certificate, error) {
	var c domain.Certificate
	dest := []any{&c.ID, &c.StudentID, &c.StudentName, &c.StudentCode, &c.LevelID, &c.ProgramName, &c.Kind, &c.ModuleID, &c.ModuleName, &c.Number, &c.Code, &c.Status, &c.IssuedOn, &c.IssuedName, &c.AnnulReason}
	if withContent {
		dest = append(dest, &c.Content)
	}
	err := row.Scan(dest...)
	return c, err
}

func (r *ProgramsRepository) Certificates(ctx context.Context, institutionID, studentID string, levelIDs []string, kind string) ([]domain.Certificate, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+certificateColumns+certificateFrom+`
		WHERE c.institution_id = $1 AND ($2 = '' OR c.student_id = nullif($2, '')::uuid) AND (coalesce(cardinality($3::uuid[]), 0) = 0 OR c.level_id = ANY($3::uuid[]))
			AND ($4 = '' OR c.kind = $4)
		ORDER BY c.issued_on DESC, c.number DESC`, institutionID, studentID, levelIDs, kind)
	if err != nil {
		return nil, programsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Certificate, error) { return scanCertificate(row, false) })
}

func (r *ProgramsRepository) Certificate(ctx context.Context, institutionID, id string) (domain.Certificate, error) {
	c, err := scanCertificate(r.c.pool.QueryRow(ctx, `SELECT `+certificateColumns+`, c.content`+certificateFrom+` WHERE c.institution_id = $1 AND c.id = $2`, institutionID, id), true)
	return c, programsErr(err)
}

func (r *ProgramsRepository) IssueCertificate(ctx context.Context, institutionID string, c domain.Certificate) (domain.Certificate, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return domain.Certificate{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1 || $2))`, institutionID, c.Kind); err != nil {
		return domain.Certificate{}, err
	}
	prefix := map[string]string{"modular": "CM", "degree": "TPT"}[c.Kind]
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO programs.certificates (institution_id, student_id, level_id, kind, module_id, number, code, content, issued_on, issued_name)
		SELECT $1, $2, $3, $4, nullif($5, '')::uuid, n, $6 || '-' || extract(year FROM $8::date)::int || '-' || lpad(n::text, 4, '0'), $7, $8, $9
		FROM (SELECT coalesce(max(number), 0) + 1 AS n FROM programs.certificates WHERE institution_id = $1 AND kind = $4) x RETURNING id`,
		institutionID, c.StudentID, c.LevelID, c.Kind, c.ModuleID, prefix, c.Content, date(c.IssuedOn), c.IssuedName).Scan(&id)
	if err != nil {
		return domain.Certificate{}, programsErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Certificate{}, err
	}
	return r.Certificate(ctx, institutionID, id)
}

func (r *ProgramsRepository) AnnulCertificate(ctx context.Context, institutionID, id, reason string) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE programs.certificates SET status = 'annulled', annul_reason = $3 WHERE institution_id = $1 AND id = $2 AND status = 'issued'`, institutionID, id, reason)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return programsErr(err)
}

func (r *ProgramsRepository) Requirements(ctx context.Context, studentID string) ([]domain.Requirement, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT code, done, note, checked_name, checked_at FROM programs.requirements WHERE student_id = $1`, studentID)
	if err != nil {
		return nil, programsErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Requirement, error) {
		var q domain.Requirement
		err := row.Scan(&q.Code, &q.Done, &q.Note, &q.CheckedName, &q.CheckedAt)
		return q, err
	})
}

func (r *ProgramsRepository) SetRequirement(ctx context.Context, studentID string, q domain.Requirement) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO programs.requirements (student_id, code, done, note, checked_name, checked_at) VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (student_id, code) DO UPDATE SET done = EXCLUDED.done, note = EXCLUDED.note, checked_name = EXCLUDED.checked_name, checked_at = now()`,
		studentID, q.Code, q.Done, q.Note, q.CheckedName)
	return programsErr(err)
}

// ---------- demo seed ----------

// SeedUnitRecords stores the history of the demo students (passed units before the platform)
func (r *ProgramsRepository) SeedUnitRecords(ctx context.Context, institutionID string, rows [][5]any) error {
	data := make([][]any, len(rows))
	for i, x := range rows {
		data[i] = []any{institutionID, x[0], x[1], x[2], x[3], x[4]}
	}
	_, err := r.c.pool.CopyFrom(ctx, pgx.Identifier{"programs", "unit_records"}, []string{"institution_id", "student_id", "subject_id", "period_name", "grade", "status"}, pgx.CopyFromRows(data))
	return programsErr(err)
}

func (r *ProgramsRepository) HasPrograms(ctx context.Context, institutionID string) (bool, error) {
	var any bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM programs.unit_records WHERE institution_id = $1) OR EXISTS (SELECT 1 FROM programs.internships WHERE institution_id = $1)`, institutionID).Scan(&any)
	return any, programsErr(err)
}

// SeedGraduates creates former students of a program (status graduated) with an old enrollment in its last
// semester, so their record has a program
func (r *ProgramsRepository) SeedGraduates(ctx context.Context, institutionID, levelID, gradeID, periodID string, people [][3]string) ([]string, error) {
	ids := make([]string, 0, len(people))
	for i, p := range people {
		var id string
		err := r.c.pool.QueryRow(ctx, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname, maternal_surname, status)
			VALUES ($1, $2, 'dni', $3, $4, $5, $6, 'graduated') RETURNING id`, institutionID, fmt.Sprintf("EGR-%03d", i+1), fmt.Sprintf("798%05d", 100+i), p[0], p[1], p[2]).Scan(&id)
		if err != nil {
			return nil, programsErr(err)
		}
		if _, err := r.c.pool.Exec(ctx, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, kind, status, enrolled_on, ended_on)
			SELECT $1, $2, $3, $4, $5, 'continuing', 'completed', p.starts_on, p.ends_on FROM academic.periods p WHERE p.id = $3`, institutionID, id, periodID, levelID, gradeID); err != nil {
			return nil, programsErr(err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// LastGrade is the last semester of a program (demo graduates)
func (r *ProgramsRepository) LastGrade(ctx context.Context, levelID string) (string, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `SELECT id FROM academic.grades WHERE level_id = $1 ORDER BY position DESC LIMIT 1`, levelID).Scan(&id)
	return id, programsErr(err)
}

// SeedLogs stores many hour logs at once
func (r *ProgramsRepository) SeedLogs(ctx context.Context, internshipID string, logs []domain.InternshipLog) error {
	rows := make([][]any, len(logs))
	for i, l := range logs {
		rows[i] = []any{internshipID, date(l.Day), l.Hours, l.Activity}
	}
	_, err := r.c.pool.CopyFrom(ctx, pgx.Identifier{"programs", "internship_logs"}, []string{"internship_id", "day", "hours", "activity"}, pgx.CopyFromRows(rows))
	return programsErr(err)
}
