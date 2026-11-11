package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// StaffRepository implements port.StaffRepository; TeachingRepository implements port.TeachingRepository
type StaffRepository struct{ c *Client }
type TeachingRepository struct{ c *Client }

func NewStaffRepository(c *Client) *StaffRepository       { return &StaffRepository{c: c} }
func NewTeachingRepository(c *Client) *TeachingRepository { return &TeachingRepository{c: c} }

var (
	_ port.StaffRepository    = (*StaffRepository)(nil)
	_ port.TeachingRepository = (*TeachingRepository)(nil)
)

func staffErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		switch pg.ConstraintName {
		case "staff_code_key":
			return domain.Validation(map[string]string{"code": "Ese código ya es de otra persona."})
		case "staff_document_key":
			return domain.Validation(map[string]string{"documentNumber": "Ya hay alguien del personal con ese documento."})
		}
	}
	return academicErr(err)
}

// ---------- staff ----------

const staffColumns = `st.id, st.code, st.document_type, st.document_number, st.given_names, st.surnames, coalesce(st.email, ''), st.phone, st.position,
	st.is_teacher, st.specialty, st.max_weekly_hours, st.hired_on, st.status, st.notes,
	coalesce((SELECT array_agg(DISTINCT r.code) FROM iam.users u JOIN iam.user_roles ur ON ur.user_id = u.id JOIN iam.roles r ON r.id = ur.role_id
		WHERE st.email IS NOT NULL AND lower(u.email) = lower(st.email) AND ur.institution_id = st.institution_id), '{}'),
	coalesce((SELECT array_agg(DISTINCT r.code) FROM iam.invitations i JOIN iam.roles r ON r.id = i.role_id
		WHERE st.email IS NOT NULL AND i.email = lower(st.email) AND i.institution_id = st.institution_id AND i.accepted_at IS NULL AND i.revoked_at IS NULL), '{}'),
	(SELECT coalesce(sum(a.weekly_hours), 0) FROM academic.assignments a JOIN academic.periods p ON p.id = a.period_id WHERE a.staff_id = st.id AND p.status = 'active'),
	coalesce((SELECT array_agg(coalesce(g.short_name || ' ', '') || s.name || ' · ' || l.name ORDER BY l.position, g.position, s.name)
		FROM academic.sections s JOIN academic.periods p ON p.id = s.period_id JOIN academic.levels l ON l.id = s.level_id LEFT JOIN academic.grades g ON g.id = s.grade_id
		WHERE s.tutor_id = st.id AND p.status = 'active'), '{}')`

func scanStaff(row pgx.Row, extra ...any) (domain.Staff, error) {
	var s domain.Staff
	var maxHours *int16
	err := row.Scan(append([]any{&s.ID, &s.Code, &s.DocumentType, &s.DocumentNumber, &s.GivenNames, &s.Surnames, &s.Email, &s.Phone, &s.Position,
		&s.IsTeacher, &s.Specialty, &maxHours, &s.HiredOn, &s.Status, &s.Notes, &s.Roles, &s.Invited, &s.AssignedHours, &s.TutorOf}, extra...)...)
	s.MaxWeeklyHours = smallPtr(maxHours)
	return s, err
}

func (r *StaffRepository) StaffList(ctx context.Context, f domain.StaffFilter) ([]domain.Staff, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > 300 {
		limit = 100
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+staffColumns+`, count(*) OVER () FROM people.staff st
		WHERE st.institution_id = $1
		  AND ($2 = '' OR `+fmt.Sprintf(fold, `st.surnames || ' ' || st.given_names || ' ' || st.given_names || ' ' || st.surnames || ' ' || st.code || ' ' || st.document_number || ' ' || st.position || ' ' || st.specialty`)+` LIKE '%' || `+fmt.Sprintf(fold, "$2")+` || '%')
		  AND ($3 = '' OR st.status = $3)
		  AND (NOT $4 OR st.is_teacher)
		ORDER BY st.surnames, st.given_names, st.id LIMIT $5 OFFSET $6`,
		f.InstitutionID, strings.TrimSpace(f.Query), f.Status, f.TeachersOnly, limit, max(f.Offset, 0))
	if err != nil {
		return nil, 0, academicErr(err)
	}
	defer rows.Close()
	var out []domain.Staff
	total := 0
	for rows.Next() {
		var t int
		s, err := scanStaff(rows, &t)
		if err != nil {
			return nil, 0, err
		}
		total = t
		out = append(out, s)
	}
	return out, total, rows.Err()
}

func (r *StaffRepository) Staff(ctx context.Context, institutionID, id string) (domain.Staff, error) {
	s, err := scanStaff(r.c.pool.QueryRow(ctx, `SELECT `+staffColumns+` FROM people.staff st WHERE st.institution_id = $1 AND st.id = $2`, institutionID, id))
	return s, academicErr(err)
}

func (r *StaffRepository) StaffByEmail(ctx context.Context, institutionID, email string) (domain.Staff, error) {
	s, err := scanStaff(r.c.pool.QueryRow(ctx, `SELECT `+staffColumns+` FROM people.staff st WHERE st.institution_id = $1 AND lower(st.email) = lower($2) AND st.status = 'active'`, institutionID, email))
	return s, academicErr(err)
}

func (r *StaffRepository) CreateStaff(ctx context.Context, institutionID string, in domain.StaffInput) (domain.Staff, error) {
	var id string
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		code := in.Code
		if code == "" {
			var next int
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(substring(code FROM '^P-([0-9]+)$')::int), 100) + 1 FROM people.staff WHERE institution_id = $1`, institutionID).Scan(&next); err != nil {
				return err
			}
			code = fmt.Sprintf("P-%04d", next)
		}
		return tx.QueryRow(ctx, `INSERT INTO people.staff (institution_id, code, document_type, document_number, given_names, surnames, email, phone, position,
				is_teacher, specialty, max_weekly_hours, hired_on, status, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING id`,
			institutionID, code, in.DocumentType, in.DocumentNumber, in.GivenNames, in.Surnames, emailOrNil(in.Email), in.Phone, in.Position,
			in.IsTeacher, in.Specialty, in.MaxWeeklyHours, dateOrNil(in.HiredOn), in.Status, in.Notes).Scan(&id)
	})
	if err != nil {
		return domain.Staff{}, staffErr(err)
	}
	return r.Staff(ctx, institutionID, id)
}

func (r *StaffRepository) UpdateStaff(ctx context.Context, institutionID, id string, in domain.StaffInput) (domain.Staff, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE people.staff SET code = $3, document_type = $4, document_number = $5, given_names = $6, surnames = $7, email = $8,
			phone = $9, position = $10, is_teacher = $11, specialty = $12, max_weekly_hours = $13, hired_on = $14, status = $15, notes = $16
		WHERE institution_id = $1 AND id = $2`,
		institutionID, id, in.Code, in.DocumentType, in.DocumentNumber, in.GivenNames, in.Surnames, emailOrNil(in.Email), in.Phone, in.Position,
		in.IsTeacher, in.Specialty, in.MaxWeeklyHours, dateOrNil(in.HiredOn), in.Status, in.Notes)
	if err != nil {
		return domain.Staff{}, staffErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Staff{}, domain.ErrNotFound
	}
	return r.Staff(ctx, institutionID, id)
}

func (r *StaffRepository) DeleteStaff(ctx context.Context, institutionID, id string) error {
	var used bool
	if err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM academic.assignments WHERE staff_id = $1) OR EXISTS (SELECT 1 FROM academic.sections WHERE tutor_id = $1)`, id).Scan(&used); err != nil {
		return academicErr(err)
	}
	if used {
		return domain.ErrStaffInUse
	}
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM people.staff WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

// ---------- time blocks ----------

func clock(t pgtype.Time) string {
	d := time.Duration(t.Microseconds) * time.Microsecond
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

func (r *TeachingRepository) TimeBlocks(ctx context.Context, institutionID string) ([]domain.TimeBlock, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, shift, position, starts_at, ends_at, kind FROM academic.time_blocks WHERE institution_id = $1
		ORDER BY CASE shift WHEN 'morning' THEN 0 WHEN 'afternoon' THEN 1 ELSE 2 END, position`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TimeBlock, error) {
		var b domain.TimeBlock
		var pos int16
		var start, end pgtype.Time
		err := row.Scan(&b.ID, &b.Shift, &pos, &start, &end, &b.Kind)
		b.Position, b.StartsAt, b.EndsAt = int(pos), clock(start), clock(end)
		return b, err
	})
}

func (r *TeachingRepository) ReplaceTimeBlocks(ctx context.Context, institutionID, shift string, blocks []domain.TimeBlock) error {
	return academicErr(pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		// Keep the ids of blocks that stay at the same position and times, so their classes survive
		rows, err := tx.Query(ctx, `SELECT id, position, to_char(starts_at, 'HH24:MI'), to_char(ends_at, 'HH24:MI'), kind FROM academic.time_blocks WHERE institution_id = $1 AND shift = $2`, institutionID, shift)
		if err != nil {
			return err
		}
		current, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TimeBlock, error) {
			var b domain.TimeBlock
			var pos int16
			err := row.Scan(&b.ID, &pos, &b.StartsAt, &b.EndsAt, &b.Kind)
			b.Position = int(pos)
			return b, err
		})
		if err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, nb := range blocks {
			for _, cb := range current {
				if cb.Position == nb.Position && cb.StartsAt == nb.StartsAt && cb.EndsAt == nb.EndsAt && cb.Kind == nb.Kind {
					keep[cb.ID] = true
				}
			}
		}
		for _, cb := range current {
			if !keep[cb.ID] {
				if _, err := tx.Exec(ctx, `DELETE FROM academic.time_blocks WHERE id = $1`, cb.ID); err != nil {
					return err
				}
			}
		}
		for _, nb := range blocks {
			if _, err := tx.Exec(ctx, `INSERT INTO academic.time_blocks (institution_id, shift, position, starts_at, ends_at, kind) VALUES ($1, $2, $3, $4::time, $5::time, $6)
				ON CONFLICT (institution_id, shift, position) DO NOTHING`, institutionID, shift, nb.Position, nb.StartsAt, nb.EndsAt, nb.Kind); err != nil {
				return err
			}
		}
		return nil
	}))
}

// ---------- assignments ----------

const assignmentColumns = `a.id, a.period_id, a.section_id, a.subject_id, sub.name, sub.kind, sub.code, coalesce(a.staff_id::text, ''),
	coalesce(st.given_names || ' ' || st.surnames, ''), a.weekly_hours, (SELECT count(*) FROM academic.schedule_entries e WHERE e.assignment_id = a.id),
	(SELECT coalesce(g.short_name || ' ', '') || sec.name || ' · ' || l.name FROM academic.sections sec JOIN academic.levels l ON l.id = sec.level_id LEFT JOIN academic.grades g ON g.id = sec.grade_id WHERE sec.id = a.section_id),
	(SELECT p.name FROM academic.periods p WHERE p.id = a.period_id)`

const assignmentFrom = ` FROM academic.assignments a JOIN academic.subjects sub ON sub.id = a.subject_id LEFT JOIN people.staff st ON st.id = a.staff_id`

func scanAssignment(row pgx.Row) (domain.Assignment, error) {
	var a domain.Assignment
	var hours int16
	err := row.Scan(&a.ID, &a.PeriodID, &a.SectionID, &a.SubjectID, &a.SubjectName, &a.SubjectKind, &a.SubjectCode, &a.StaffID, &a.StaffName, &hours, &a.Scheduled, &a.SectionLabel, &a.PeriodName)
	a.WeeklyHours = int(hours)
	return a, err
}

func (r *TeachingRepository) Assignments(ctx context.Context, institutionID, periodID string) ([]domain.Assignment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+assignmentColumns+assignmentFrom+` WHERE a.institution_id = $1 AND a.period_id = $2 ORDER BY a.section_id, sub.position`, institutionID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Assignment, error) { return scanAssignment(row) })
}

func (r *TeachingRepository) Assignment(ctx context.Context, institutionID, id string) (domain.Assignment, error) {
	a, err := scanAssignment(r.c.pool.QueryRow(ctx, `SELECT `+assignmentColumns+assignmentFrom+` WHERE a.institution_id = $1 AND a.id = $2`, institutionID, id))
	return a, academicErr(err)
}

func (r *TeachingRepository) StaffAssignments(ctx context.Context, institutionID, staffID string) ([]domain.Assignment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+assignmentColumns+assignmentFrom+` JOIN academic.periods p ON p.id = a.period_id
		WHERE a.institution_id = $1 AND a.staff_id = $2 AND p.status <> 'closed' ORDER BY p.starts_on DESC, sub.name`, institutionID, staffID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Assignment, error) { return scanAssignment(row) })
}

func (r *TeachingRepository) CreateAssignments(ctx context.Context, institutionID string, items []domain.Assignment) (int, error) {
	added := 0
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		for _, a := range items {
			tag, err := tx.Exec(ctx, `INSERT INTO academic.assignments (institution_id, period_id, section_id, subject_id, staff_id, weekly_hours)
				SELECT $1, s.period_id, s.id, $3, $4, $5 FROM academic.sections s JOIN academic.periods p ON p.id = s.period_id
				WHERE s.id = $2 AND p.institution_id = $1
				ON CONFLICT (section_id, subject_id) DO NOTHING`, institutionID, a.SectionID, a.SubjectID, uuidOrNil(a.StaffID), a.WeeklyHours)
			if err != nil {
				return err
			}
			added += int(tag.RowsAffected())
		}
		return nil
	})
	return added, academicErr(err)
}

func (r *TeachingRepository) UpdateAssignment(ctx context.Context, institutionID, id, staffID string, weeklyHours int) (domain.Assignment, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.assignments SET staff_id = $3, weekly_hours = $4 WHERE institution_id = $1 AND id = $2`, institutionID, id, uuidOrNil(staffID), weeklyHours)
	if err != nil {
		return domain.Assignment{}, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Assignment{}, domain.ErrNotFound
	}
	return r.Assignment(ctx, institutionID, id)
}

func (r *TeachingRepository) DeleteAssignment(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.assignments WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *TeachingRepository) SetTutor(ctx context.Context, institutionID, sectionID, staffID string) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE academic.sections s SET tutor_id = $3 FROM academic.periods p
		WHERE p.id = s.period_id AND p.institution_id = $1 AND s.id = $2`, institutionID, sectionID, uuidOrNil(staffID))
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

// ---------- timetable ----------

const entryColumns = `e.id, e.period_id, e.section_id, coalesce(g.short_name || ' ', '') || sec.name, sec.shift, e.assignment_id, sub.name,
	coalesce(a.staff_id::text, ''), coalesce(st.given_names || ' ' || st.surnames, ''), e.weekday, e.block_id,
	to_char(b.starts_at, 'HH24:MI'), to_char(b.ends_at, 'HH24:MI'), e.room`

const entryFrom = ` FROM academic.schedule_entries e
	JOIN academic.assignments a ON a.id = e.assignment_id
	JOIN academic.subjects sub ON sub.id = a.subject_id
	JOIN academic.sections sec ON sec.id = e.section_id
	LEFT JOIN academic.grades g ON g.id = sec.grade_id
	JOIN academic.time_blocks b ON b.id = e.block_id
	LEFT JOIN people.staff st ON st.id = a.staff_id`

func scanEntry(row pgx.Row) (domain.ScheduleEntry, error) {
	var e domain.ScheduleEntry
	var weekday int16
	err := row.Scan(&e.ID, &e.PeriodID, &e.SectionID, &e.SectionLabel, &e.Shift, &e.AssignmentID, &e.SubjectName, &e.StaffID, &e.StaffName, &weekday, &e.BlockID, &e.StartsAt, &e.EndsAt, &e.Room)
	e.Weekday = int(weekday)
	return e, err
}

func (r *TeachingRepository) entries(ctx context.Context, where string, args ...any) ([]domain.ScheduleEntry, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+entryColumns+entryFrom+` WHERE `+where+` ORDER BY e.weekday, b.starts_at`, args...)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ScheduleEntry, error) { return scanEntry(row) })
}

func (r *TeachingRepository) Entries(ctx context.Context, institutionID string, periodIDs []string) ([]domain.ScheduleEntry, error) {
	return r.entries(ctx, `e.institution_id = $1 AND e.period_id = ANY ($2::uuid[])`, institutionID, periodIDs)
}

func (r *TeachingRepository) StaffEntries(ctx context.Context, institutionID, staffID string, periodIDs []string) ([]domain.ScheduleEntry, error) {
	return r.entries(ctx, `e.institution_id = $1 AND a.staff_id = $2 AND e.period_id = ANY ($3::uuid[])`, institutionID, staffID, periodIDs)
}

func (r *TeachingRepository) SectionEntries(ctx context.Context, institutionID, sectionID string) ([]domain.ScheduleEntry, error) {
	return r.entries(ctx, `e.institution_id = $1 AND e.section_id = $2`, institutionID, sectionID)
}

func (r *TeachingRepository) PlaceEntry(ctx context.Context, institutionID, periodID string, slot domain.Slot, room string) (domain.ScheduleEntry, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO academic.schedule_entries (institution_id, period_id, section_id, assignment_id, weekday, block_id, room)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (section_id, weekday, block_id) DO UPDATE SET assignment_id = EXCLUDED.assignment_id, room = EXCLUDED.room
		RETURNING id`, institutionID, periodID, slot.SectionID, slot.AssignmentID, slot.Weekday, slot.BlockID, room).Scan(&id)
	if err != nil {
		return domain.ScheduleEntry{}, academicErr(err)
	}
	list, err := r.entries(ctx, `e.id = $1`, id)
	if err != nil || len(list) == 0 {
		return domain.ScheduleEntry{}, err
	}
	return list[0], nil
}

func (r *TeachingRepository) DeleteEntry(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM academic.schedule_entries WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *TeachingRepository) PlaceEntries(ctx context.Context, institutionID, periodID string, slots []domain.Slot) (int, error) {
	added := 0
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		for _, s := range slots {
			tag, err := tx.Exec(ctx, `INSERT INTO academic.schedule_entries (institution_id, period_id, section_id, assignment_id, weekday, block_id)
				VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`, institutionID, periodID, s.SectionID, s.AssignmentID, s.Weekday, s.BlockID)
			if err != nil {
				return err
			}
			added += int(tag.RowsAffected())
		}
		return nil
	})
	return added, academicErr(err)
}

func (r *TeachingRepository) ClearSection(ctx context.Context, institutionID, sectionID string) error {
	_, err := r.c.pool.Exec(ctx, `DELETE FROM academic.schedule_entries WHERE institution_id = $1 AND section_id = $2`, institutionID, sectionID)
	return academicErr(err)
}

func (r *TeachingRepository) SectionsForEmail(ctx context.Context, institutionID, email string) ([]domain.PersonalSection, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.given_names || ' ' || s.paternal_surname, sec.id, coalesce(g.short_name || ' ', '') || sec.name, l.name, p.id, p.name, sec.shift
		FROM people.students s
		JOIN enrollment.enrollments e ON e.student_id = s.id AND e.status = 'active'
		JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
		JOIN academic.sections sec ON sec.id = e.section_id
		JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id
		WHERE s.institution_id = $1 AND (lower(s.email) = lower($2) OR EXISTS (
			SELECT 1 FROM people.student_guardians sg JOIN people.guardians gu ON gu.id = sg.guardian_id WHERE sg.student_id = s.id AND lower(gu.email) = lower($2)))
		ORDER BY s.given_names`, institutionID, email)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PersonalSection, error) {
		var x domain.PersonalSection
		err := row.Scan(&x.StudentName, &x.SectionID, &x.SectionLabel, &x.LevelName, &x.PeriodID, &x.PeriodName, &x.Shift)
		return x, err
	})
}
