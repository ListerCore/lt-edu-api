package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// PeopleRepository implements port.PeopleRepository over the people and enrollment schemas
type PeopleRepository struct {
	c *Client
}

func NewPeopleRepository(c *Client) *PeopleRepository {
	return &PeopleRepository{c: c}
}

var _ port.PeopleRepository = (*PeopleRepository)(nil)

func uuidOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func emailOrNil(s string) *string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	return &s
}

func dateOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	d := date(*t)
	return &d
}

// fold is the accent-insensitive lower case used to search names
const fold = `translate(lower(%s), 'áéíóúüñ', 'aeiouun')`

// accessSQL tells whether an email has the role in the institution (account), a pending invitation, or nothing
func accessSQL(emailExpr, institutionExpr, role string) string {
	return fmt.Sprintf(`CASE
		WHEN %[1]s IS NULL THEN 'none'
		WHEN EXISTS (SELECT 1 FROM iam.users u JOIN iam.user_roles ur ON ur.user_id = u.id JOIN iam.roles r ON r.id = ur.role_id
			WHERE lower(u.email) = lower(%[1]s) AND ur.institution_id = %[2]s AND r.code = '%[3]s') THEN 'account'
		WHEN EXISTS (SELECT 1 FROM iam.invitations i JOIN iam.roles r ON r.id = i.role_id
			WHERE i.institution_id = %[2]s AND i.email = lower(%[1]s) AND r.code = '%[3]s' AND i.accepted_at IS NULL AND i.revoked_at IS NULL) THEN 'invited'
		ELSE 'none' END`, emailExpr, institutionExpr, role)
}

// ---------- students ----------

var studentColumns = `s.id, s.institution_id, s.code, s.document_type, s.document_number, s.given_names, s.paternal_surname, s.maternal_surname,
	s.birth_date, coalesce(s.sex, ''), coalesce(s.email, ''), s.phone, s.address, s.special_needs, s.notes, s.status, s.created_at, ` + accessSQL("s.email", "s.institution_id", "student")

func scanStudentInto(s *domain.Student) []any {
	return []any{&s.ID, &s.InstitutionID, &s.Code, &s.DocumentType, &s.DocumentNumber, &s.GivenNames, &s.PaternalSurname, &s.MaternalSurname,
		&s.BirthDate, &s.Sex, &s.Email, &s.Phone, &s.Address, &s.SpecialNeeds, &s.Notes, &s.Status, &s.CreatedAt, &s.Access}
}

// The enrollment of the student in a period (or the latest one) and the main guardian
const studentExtras = `
	LEFT JOIN LATERAL (
		SELECT e.id, e.period_id, p.name AS period_name, e.level_id, l.name AS level_name, coalesce(e.grade_id::text, '') AS grade_id,
			coalesce(g.name, '') AS grade_name, coalesce(g.short_name, '') AS grade_short, coalesce(e.section_id::text, '') AS section_id,
			coalesce(sec.name, '') AS section_name, coalesce(sec.shift, '') AS shift, e.status
		FROM enrollment.enrollments e
		JOIN academic.periods p ON p.id = e.period_id
		JOIN academic.levels l ON l.id = e.level_id
		LEFT JOIN academic.grades g ON g.id = e.grade_id
		LEFT JOIN academic.sections sec ON sec.id = e.section_id
		WHERE e.student_id = s.id AND ($2::uuid IS NULL OR e.period_id = $2::uuid)
		ORDER BY p.starts_on DESC LIMIT 1
	) en ON true
	LEFT JOIN LATERAL (
		SELECT gu.given_names || ' ' || gu.surnames AS name, gu.phone, (SELECT count(*) FROM people.student_guardians x WHERE x.student_id = s.id) AS total
		FROM people.student_guardians sg JOIN people.guardians gu ON gu.id = sg.guardian_id
		WHERE sg.student_id = s.id ORDER BY sg.is_primary DESC, sg.created_at LIMIT 1
	) pg ON true`

const studentExtraColumns = `, en.id, en.period_id, en.period_name, en.level_id, en.level_name, en.grade_id, en.grade_name, en.grade_short,
	en.section_id, en.section_name, en.shift, en.status, coalesce(pg.name, ''), coalesce(pg.phone, ''), coalesce(pg.total, 0)`

func scanStudentRow(row pgx.Row, withTotal bool) (domain.Student, int, error) {
	var s domain.Student
	var enID, enPeriod, enPeriodName, enLevel, enLevelName, enGrade, enGradeName, enGradeShort, enSection, enSectionName, enShift, enStatus *string
	var total int
	dest := append(scanStudentInto(&s), &enID, &enPeriod, &enPeriodName, &enLevel, &enLevelName, &enGrade, &enGradeName, &enGradeShort, &enSection, &enSectionName, &enShift, &enStatus, &s.PrimaryName, &s.PrimaryPhone, &s.GuardianCount)
	if withTotal {
		dest = append(dest, &total)
	}
	if err := row.Scan(dest...); err != nil {
		return s, 0, err
	}
	if enID != nil {
		s.Enrollment = &domain.EnrollmentSummary{ID: *enID, PeriodID: *enPeriod, PeriodName: *enPeriodName, LevelID: *enLevel, LevelName: *enLevelName,
			GradeID: *enGrade, GradeName: *enGradeName, GradeShort: *enGradeShort, SectionID: *enSection, SectionName: *enSectionName, Shift: *enShift, Status: *enStatus}
	}
	return s, total, nil
}

func (r *PeopleRepository) Students(ctx context.Context, f domain.StudentFilter) ([]domain.Student, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := strings.TrimSpace(f.Query)
	rows, err := r.c.pool.Query(ctx, `SELECT `+studentColumns+studentExtraColumns+`, count(*) OVER ()
		FROM people.students s`+studentExtras+`
		WHERE s.institution_id = $1
		  AND ($3 = '' OR `+fmt.Sprintf(fold, `s.paternal_surname || ' ' || s.maternal_surname || ' ' || s.given_names || ' ' || s.given_names || ' ' || s.paternal_surname || ' ' || s.code || ' ' || s.document_number`)+` LIKE '%' || `+fmt.Sprintf(fold, "$3")+` || '%')
		  AND ($4 = '' OR s.status = $4)
		  AND ($5::uuid IS NULL OR en.level_id = $5::uuid)
		  AND ($6::uuid IS NULL OR en.grade_id = ($6::uuid)::text)
		  AND ($7::uuid IS NULL OR en.section_id = ($7::uuid)::text)
		  AND (NOT $8 OR en.id IS NULL)
		ORDER BY s.paternal_surname, s.maternal_surname, s.given_names, s.id
		LIMIT $9 OFFSET $10`,
		f.InstitutionID, uuidOrNil(f.PeriodID), query, f.Status, uuidOrNil(f.LevelID), uuidOrNil(f.GradeID), uuidOrNil(f.SectionID), f.NotEnrolled, limit, max(f.Offset, 0))
	if err != nil {
		return nil, 0, academicErr(err)
	}
	defer rows.Close()
	var out []domain.Student
	total := 0
	for rows.Next() {
		s, t, err := scanStudentRow(rows, true)
		if err != nil {
			return nil, 0, err
		}
		total = t
		out = append(out, s)
	}
	return out, total, rows.Err()
}

func (r *PeopleRepository) Student(ctx context.Context, institutionID, id string) (domain.Student, error) {
	s, _, err := scanStudentRow(r.c.pool.QueryRow(ctx, `SELECT `+studentColumns+studentExtraColumns+`
		FROM people.students s`+studentExtras+` WHERE s.institution_id = $1 AND s.id = $3::uuid`, institutionID, nil, id), false)
	return s, academicErr(err)
}

// peopleErr maps the unique violations of people to clear messages
func peopleErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		switch pg.ConstraintName {
		case "students_code_key":
			return domain.Validation(map[string]string{"code": "Ese código ya es de otro estudiante."})
		case "students_document_key":
			return domain.Validation(map[string]string{"documentNumber": "Ya hay un estudiante con ese documento."})
		case "guardians_document_key":
			return domain.Validation(map[string]string{"documentNumber": "Ya hay un apoderado con ese documento."})
		case "enrollments_student_period_key":
			return domain.ErrAlreadyEnrolled
		}
	}
	return academicErr(err)
}

func (r *PeopleRepository) CreateStudent(ctx context.Context, institutionID string, in domain.StudentInput) (domain.Student, error) {
	var id string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		code := in.Code
		if code == "" {
			year := time.Now().Year()
			var next int
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(substring(code FROM '^[0-9]{4}-([0-9]+)$')::int), 0) + 1 FROM people.students
				WHERE institution_id = $1 AND code LIKE $2 || '-%'`, institutionID, fmt.Sprint(year)).Scan(&next); err != nil {
				return err
			}
			code = fmt.Sprintf("%d-%04d", year, next)
		}
		return tx.QueryRow(ctx, `INSERT INTO people.students (institution_id, code, document_type, document_number, given_names, paternal_surname, maternal_surname,
				birth_date, sex, email, phone, address, special_needs, notes, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id`,
			institutionID, code, in.DocumentType, in.DocumentNumber, in.GivenNames, in.PaternalSurname, in.MaternalSurname, dateOrNil(in.BirthDate),
			nilIfEmpty(in.Sex), emailOrNil(in.Email), in.Phone, in.Address, in.SpecialNeeds, in.Notes, in.Status).Scan(&id)
	})
	if err != nil {
		return domain.Student{}, peopleErr(err)
	}
	return r.Student(ctx, institutionID, id)
}

func (r *PeopleRepository) UpdateStudent(ctx context.Context, institutionID, id string, in domain.StudentInput) (domain.Student, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE people.students SET code = $3, document_type = $4, document_number = $5, given_names = $6, paternal_surname = $7,
			maternal_surname = $8, birth_date = $9, sex = $10, email = $11, phone = $12, address = $13, special_needs = $14, notes = $15, status = $16
		WHERE institution_id = $1 AND id = $2`,
		institutionID, id, in.Code, in.DocumentType, in.DocumentNumber, in.GivenNames, in.PaternalSurname, in.MaternalSurname, dateOrNil(in.BirthDate),
		nilIfEmpty(in.Sex), emailOrNil(in.Email), in.Phone, in.Address, in.SpecialNeeds, in.Notes, in.Status)
	if err != nil {
		return domain.Student{}, peopleErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Student{}, domain.ErrNotFound
	}
	return r.Student(ctx, institutionID, id)
}

func (r *PeopleRepository) DeleteStudent(ctx context.Context, institutionID, id string) error {
	return academicErr(pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		var enrolled bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM enrollment.enrollments WHERE student_id = $1)`, id).Scan(&enrolled); err != nil {
			return err
		}
		if enrolled {
			return domain.ErrStudentEnrolled
		}
		// Guardians that answered only for this student go with it
		if _, err := tx.Exec(ctx, `DELETE FROM people.guardians g USING people.student_guardians sg
			WHERE sg.guardian_id = g.id AND sg.student_id = $2 AND g.institution_id = $1
			  AND NOT EXISTS (SELECT 1 FROM people.student_guardians o WHERE o.guardian_id = g.id AND o.student_id <> $2)`, institutionID, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM people.students WHERE institution_id = $1 AND id = $2`, institutionID, id)
		if err == nil && tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		return err
	}))
}

// ---------- guardians ----------

var guardianColumns = `g.id, g.document_type, g.document_number, g.given_names, g.surnames, coalesce(g.email, ''), g.phone, g.occupation, ` +
	accessSQL("g.email", "g.institution_id", "guardian") + `, (SELECT count(*) FROM people.student_guardians x WHERE x.guardian_id = g.id)`

func scanGuardian(row pgx.Row, withLink bool) (domain.Guardian, error) {
	var g domain.Guardian
	dest := []any{&g.ID, &g.DocumentType, &g.DocumentNumber, &g.GivenNames, &g.Surnames, &g.Email, &g.Phone, &g.Occupation, &g.Access, &g.StudentCount}
	if withLink {
		dest = append(dest, &g.Relationship, &g.IsPrimary, &g.CanPickUp, &g.LivesWith)
	}
	err := row.Scan(dest...)
	return g, err
}

func (r *PeopleRepository) StudentGuardians(ctx context.Context, institutionID, studentID string) ([]domain.Guardian, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+guardianColumns+`, sg.relationship, sg.is_primary, sg.can_pick_up, sg.lives_with
		FROM people.student_guardians sg JOIN people.guardians g ON g.id = sg.guardian_id
		WHERE g.institution_id = $1 AND sg.student_id = $2 ORDER BY sg.is_primary DESC, sg.created_at`, institutionID, studentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Guardian, error) { return scanGuardian(row, true) })
}

func (r *PeopleRepository) Guardian(ctx context.Context, institutionID, id string) (domain.Guardian, error) {
	g, err := scanGuardian(r.c.pool.QueryRow(ctx, `SELECT `+guardianColumns+` FROM people.guardians g WHERE g.institution_id = $1 AND g.id = $2`, institutionID, id), false)
	return g, academicErr(err)
}

func (r *PeopleRepository) GuardianByDocument(ctx context.Context, institutionID, documentType, documentNumber string) (domain.Guardian, error) {
	g, err := scanGuardian(r.c.pool.QueryRow(ctx, `SELECT `+guardianColumns+` FROM people.guardians g
		WHERE g.institution_id = $1 AND g.document_type = $2 AND g.document_number = $3`, institutionID, documentType, documentNumber), false)
	return g, academicErr(err)
}

func (r *PeopleRepository) CreateGuardian(ctx context.Context, institutionID string, in domain.GuardianInput) (domain.Guardian, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO people.guardians (institution_id, document_type, document_number, given_names, surnames, email, phone, occupation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		institutionID, in.DocumentType, in.DocumentNumber, in.GivenNames, in.Surnames, emailOrNil(in.Email), in.Phone, in.Occupation).Scan(&id)
	if err != nil {
		return domain.Guardian{}, peopleErr(err)
	}
	return r.Guardian(ctx, institutionID, id)
}

func (r *PeopleRepository) UpdateGuardian(ctx context.Context, institutionID, id string, in domain.GuardianInput) (domain.Guardian, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE people.guardians SET document_type = $3, document_number = $4, given_names = $5, surnames = $6, email = $7, phone = $8, occupation = $9
		WHERE institution_id = $1 AND id = $2`, institutionID, id, in.DocumentType, in.DocumentNumber, in.GivenNames, in.Surnames, emailOrNil(in.Email), in.Phone, in.Occupation)
	if err != nil {
		return domain.Guardian{}, peopleErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Guardian{}, domain.ErrNotFound
	}
	return r.Guardian(ctx, institutionID, id)
}

func (r *PeopleRepository) IsLinked(ctx context.Context, studentID, guardianID string) (bool, error) {
	var linked bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people.student_guardians WHERE student_id = $1 AND guardian_id = $2)`, studentID, guardianID).Scan(&linked)
	return linked, academicErr(err)
}

func (r *PeopleRepository) LinkGuardian(ctx context.Context, institutionID, studentID, guardianID string, link domain.GuardianLink) error {
	return academicErr(pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM people.students WHERE institution_id = $1 AND id = $2)
			AND EXISTS (SELECT 1 FROM people.guardians WHERE institution_id = $1 AND id = $3)`, institutionID, studentID, guardianID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return domain.ErrNotFound
		}
		if link.IsPrimary {
			if _, err := tx.Exec(ctx, `UPDATE people.student_guardians SET is_primary = false WHERE student_id = $1 AND guardian_id <> $2`, studentID, guardianID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO people.student_guardians (student_id, guardian_id, relationship, is_primary, can_pick_up, lives_with)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (student_id, guardian_id) DO UPDATE SET relationship = EXCLUDED.relationship, is_primary = EXCLUDED.is_primary,
				can_pick_up = EXCLUDED.can_pick_up, lives_with = EXCLUDED.lives_with`,
			studentID, guardianID, link.Relationship, link.IsPrimary, link.CanPickUp, link.LivesWith)
		if err != nil {
			return err
		}
		// A student with guardians always has a main contact
		_, err = tx.Exec(ctx, `UPDATE people.student_guardians SET is_primary = true
			WHERE student_id = $1 AND guardian_id = (SELECT guardian_id FROM people.student_guardians WHERE student_id = $1 ORDER BY created_at LIMIT 1)
			  AND NOT EXISTS (SELECT 1 FROM people.student_guardians WHERE student_id = $1 AND is_primary)`, studentID)
		return err
	}))
}

func (r *PeopleRepository) UnlinkGuardian(ctx context.Context, institutionID, studentID, guardianID string) error {
	return academicErr(pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM people.student_guardians sg USING people.students s
			WHERE s.id = sg.student_id AND s.institution_id = $1 AND sg.student_id = $2 AND sg.guardian_id = $3`, institutionID, studentID, guardianID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM people.guardians g WHERE g.id = $1 AND NOT EXISTS (SELECT 1 FROM people.student_guardians WHERE guardian_id = g.id)`, guardianID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE people.student_guardians SET is_primary = true
			WHERE student_id = $1 AND guardian_id = (SELECT guardian_id FROM people.student_guardians WHERE student_id = $1 ORDER BY created_at LIMIT 1)
			  AND NOT EXISTS (SELECT 1 FROM people.student_guardians WHERE student_id = $1 AND is_primary)`, studentID)
		return err
	}))
}

func (r *PeopleRepository) GrantAccess(ctx context.Context, institutionID, email, roleCode, invitedBy string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var access string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		var userID *string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM iam.users WHERE lower(email) = $1`, email).Scan(&userID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if userID != nil {
			access = domain.AccessAccount
			_, err := tx.Exec(ctx, `INSERT INTO iam.user_roles (user_id, role_id, role_scope, institution_id, granted_by)
				SELECT $1, r.id, 'institution', $2, $4 FROM iam.roles r WHERE r.code = $3
				ON CONFLICT DO NOTHING`, *userID, institutionID, roleCode, uuidOrNil(invitedBy))
			return err
		}
		access = domain.AccessInvited
		_, err := tx.Exec(ctx, `INSERT INTO iam.invitations (institution_id, email, role_id, invited_by)
			SELECT $1, $2, r.id, $4 FROM iam.roles r WHERE r.code = $3
			ON CONFLICT DO NOTHING`, institutionID, email, roleCode, uuidOrNil(invitedBy))
		return err
	})
	return access, academicErr(err)
}

// ---------- enrollments ----------

const enrollmentColumns = `e.id, e.student_id, s.paternal_surname || CASE WHEN s.maternal_surname <> '' THEN ' ' || s.maternal_surname ELSE '' END || ', ' || s.given_names,
	s.code, s.document_number, e.period_id, p.name, e.level_id, l.name, coalesce(e.grade_id::text, ''), coalesce(g.name, ''), coalesce(g.short_name, ''),
	coalesce(e.section_id::text, ''), coalesce(sec.name, ''), coalesce(sec.shift, ''), coalesce(sec.attention_mode, ''), e.kind, e.status, e.enrolled_on, e.ended_on,
	e.origin, e.destination, e.notes,
	coalesce((SELECT array_agg(ue.subject_id::text ORDER BY sub.position) FROM enrollment.unit_enrollments ue JOIN academic.subjects sub ON sub.id = ue.subject_id WHERE ue.enrollment_id = e.id), '{}'),
	(SELECT coalesce(sum(coalesce(sub.theory_credits, 0) + coalesce(sub.practice_credits, 0)), 0) FROM enrollment.unit_enrollments ue JOIN academic.subjects sub ON sub.id = ue.subject_id WHERE ue.enrollment_id = e.id)`

const enrollmentFrom = ` FROM enrollment.enrollments e
	JOIN people.students s ON s.id = e.student_id
	JOIN academic.periods p ON p.id = e.period_id
	JOIN academic.levels l ON l.id = e.level_id
	LEFT JOIN academic.grades g ON g.id = e.grade_id
	LEFT JOIN academic.sections sec ON sec.id = e.section_id`

func scanEnrollment(row pgx.Row, extra ...any) (domain.Enrollment, error) {
	var e domain.Enrollment
	dest := append([]any{&e.ID, &e.StudentID, &e.StudentName, &e.StudentCode, &e.StudentDoc, &e.PeriodID, &e.PeriodName, &e.LevelID, &e.LevelName, &e.GradeID, &e.GradeName, &e.GradeShort,
		&e.SectionID, &e.SectionName, &e.Shift, &e.AttentionMode, &e.Kind, &e.Status, &e.EnrolledOn, &e.EndedOn, &e.Origin, &e.Destination, &e.Notes, &e.UnitIDs, &e.Credits}, extra...)
	err := row.Scan(dest...)
	return e, err
}

func (r *PeopleRepository) Enrollments(ctx context.Context, f domain.EnrollmentFilter) ([]domain.Enrollment, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+enrollmentColumns+`, count(*) OVER ()`+enrollmentFrom+`
		WHERE e.institution_id = $1 AND e.period_id = $2
		  AND ($3::uuid IS NULL OR e.level_id = $3::uuid)
		  AND ($4::uuid IS NULL OR e.grade_id = $4::uuid)
		  AND ($5::uuid IS NULL OR e.section_id = $5::uuid)
		  AND ($6 = '' OR e.status = $6)
		  AND ($7 = '' OR `+fmt.Sprintf(fold, `s.paternal_surname || ' ' || s.maternal_surname || ' ' || s.given_names || ' ' || s.given_names || ' ' || s.paternal_surname || ' ' || s.code || ' ' || s.document_number`)+` LIKE '%' || `+fmt.Sprintf(fold, "$7")+` || '%')
		ORDER BY l.position, g.position NULLS FIRST, sec.name NULLS FIRST, s.paternal_surname, s.maternal_surname, s.given_names
		LIMIT $8 OFFSET $9`,
		f.InstitutionID, f.PeriodID, uuidOrNil(f.LevelID), uuidOrNil(f.GradeID), uuidOrNil(f.SectionID), f.Status, strings.TrimSpace(f.Query), limit, max(f.Offset, 0))
	if err != nil {
		return nil, 0, academicErr(err)
	}
	defer rows.Close()
	var out []domain.Enrollment
	total := 0
	for rows.Next() {
		var t int
		e, err := scanEnrollment(rows, &t)
		if err != nil {
			return nil, 0, err
		}
		total = t
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func (r *PeopleRepository) Enrollment(ctx context.Context, institutionID, id string) (domain.Enrollment, error) {
	e, err := scanEnrollment(r.c.pool.QueryRow(ctx, `SELECT `+enrollmentColumns+enrollmentFrom+` WHERE e.institution_id = $1 AND e.id = $2`, institutionID, id))
	return e, academicErr(err)
}

func (r *PeopleRepository) EnrollmentOf(ctx context.Context, institutionID, studentID, periodID string) (domain.Enrollment, error) {
	e, err := scanEnrollment(r.c.pool.QueryRow(ctx, `SELECT `+enrollmentColumns+enrollmentFrom+` WHERE e.institution_id = $1 AND e.student_id = $2 AND e.period_id = $3`, institutionID, studentID, periodID))
	return e, academicErr(err)
}

func (r *PeopleRepository) StudentEnrollments(ctx context.Context, institutionID, studentID string) ([]domain.Enrollment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+enrollmentColumns+enrollmentFrom+` WHERE e.institution_id = $1 AND e.student_id = $2 ORDER BY p.starts_on DESC`, institutionID, studentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Enrollment, error) { return scanEnrollment(row) })
}

func setUnits(ctx context.Context, tx pgx.Tx, enrollmentID string, unitIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM enrollment.unit_enrollments WHERE enrollment_id = $1`, enrollmentID); err != nil {
		return err
	}
	if len(unitIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO enrollment.unit_enrollments (enrollment_id, subject_id) SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, enrollmentID, unitIDs)
	return err
}

func (r *PeopleRepository) CreateEnrollment(ctx context.Context, institutionID string, in domain.EnrollmentInput) (domain.Enrollment, error) {
	var id string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO enrollment.enrollments (institution_id, student_id, period_id, level_id, grade_id, section_id, kind, enrolled_on, origin, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			institutionID, in.StudentID, in.PeriodID, in.LevelID, uuidOrNil(in.GradeID), uuidOrNil(in.SectionID), in.Kind, date(in.EnrolledOn), in.Origin, in.Notes).Scan(&id); err != nil {
			return err
		}
		return setUnits(ctx, tx, id, in.UnitIDs)
	})
	if err != nil {
		return domain.Enrollment{}, peopleErr(err)
	}
	return r.Enrollment(ctx, institutionID, id)
}

func (r *PeopleRepository) UpdateEnrollment(ctx context.Context, institutionID, id, gradeID, sectionID, notes string, unitIDs []string) (domain.Enrollment, error) {
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE enrollment.enrollments SET grade_id = $3, section_id = $4, notes = $5 WHERE institution_id = $1 AND id = $2`,
			institutionID, id, uuidOrNil(gradeID), uuidOrNil(sectionID), notes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		if unitIDs != nil {
			return setUnits(ctx, tx, id, unitIDs)
		}
		return nil
	})
	if err != nil {
		return domain.Enrollment{}, peopleErr(err)
	}
	return r.Enrollment(ctx, institutionID, id)
}

func (r *PeopleRepository) SetEnrollmentStatus(ctx context.Context, institutionID, id, status string, endedOn *time.Time, destination string) (domain.Enrollment, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE enrollment.enrollments SET status = $3, ended_on = $4, destination = $5 WHERE institution_id = $1 AND id = $2`,
		institutionID, id, status, dateOrNil(endedOn), destination)
	if err != nil {
		return domain.Enrollment{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Enrollment{}, domain.ErrNotFound
	}
	return r.Enrollment(ctx, institutionID, id)
}

func (r *PeopleRepository) DeleteEnrollment(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM enrollment.enrollments WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *PeopleRepository) Occupancy(ctx context.Context, institutionID, periodID string) ([]domain.SectionOccupancy, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT sec.id, (SELECT count(*) FROM enrollment.enrollments e WHERE e.section_id = sec.id AND e.status = 'active'), sec.capacity
		FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id
		WHERE p.institution_id = $1 AND sec.period_id = $2`, institutionID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SectionOccupancy, error) {
		var o domain.SectionOccupancy
		var capacity int16
		err := row.Scan(&o.SectionID, &o.Active, &capacity)
		o.Capacity = int(capacity)
		return o, err
	})
}

// ---------- placements ----------

const placementSelect = `SELECT pl.id, pl.student_id, s.paternal_surname || ' ' || s.maternal_surname || ', ' || s.given_names, pl.kind, pl.level_id, l.name, pl.grade_id, g.name,
	pl.decided_on, pl.reference, pl.notes
	FROM enrollment.placements pl
	JOIN people.students s ON s.id = pl.student_id
	JOIN academic.levels l ON l.id = pl.level_id
	JOIN academic.grades g ON g.id = pl.grade_id`

func scanPlacement(row pgx.Row) (domain.Placement, error) {
	var p domain.Placement
	err := row.Scan(&p.ID, &p.StudentID, &p.StudentName, &p.Kind, &p.LevelID, &p.LevelName, &p.GradeID, &p.GradeName, &p.DecidedOn, &p.Reference, &p.Notes)
	return p, err
}

func (r *PeopleRepository) Placements(ctx context.Context, institutionID, studentID string) ([]domain.Placement, error) {
	rows, err := r.c.pool.Query(ctx, placementSelect+` WHERE pl.institution_id = $1 AND ($2::uuid IS NULL OR pl.student_id = $2::uuid) ORDER BY pl.decided_on DESC, pl.created_at DESC LIMIT 500`,
		institutionID, uuidOrNil(studentID))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Placement, error) { return scanPlacement(row) })
}

func (r *PeopleRepository) CreatePlacement(ctx context.Context, institutionID string, in domain.PlacementInput) (domain.Placement, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO enrollment.placements (institution_id, student_id, kind, level_id, grade_id, decided_on, reference, notes)
		SELECT $1, s.id, $3, $4, $5, $6, $7, $8 FROM people.students s WHERE s.institution_id = $1 AND s.id = $2 RETURNING id`,
		institutionID, in.StudentID, in.Kind, in.LevelID, in.GradeID, date(in.DecidedOn), in.Reference, in.Notes).Scan(&id)
	if err != nil {
		return domain.Placement{}, academicErr(err)
	}
	return scanPlacement(r.c.pool.QueryRow(ctx, placementSelect+` WHERE pl.id = $1`, id))
}

func (r *PeopleRepository) DeletePlacement(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM enrollment.placements WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}
