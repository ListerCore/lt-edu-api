package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// AttendanceRepository implements port.AttendanceRepository
type AttendanceRepository struct{ c *Client }

func NewAttendanceRepository(c *Client) *AttendanceRepository { return &AttendanceRepository{c: c} }

var _ port.AttendanceRepository = (*AttendanceRepository)(nil)

// sectionLabel is "3.° B · Secundaria" for a section aliased sec with its grade g and level l
const sectionLabel = `coalesce(g.short_name || ' ', '') || sec.name || ' · ' || l.name`

// sectionsOn are the sections of active periods whose dates cover $2 in institution $1
const sectionsOn = `SELECT sec.id FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id
	WHERE p.institution_id = $1 AND p.status = 'active' AND $2::date BETWEEN p.starts_on AND p.ends_on`

func studentName() string {
	return `s.given_names || ' ' || s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '')`
}

// ---------- ID cards ----------

func (r *AttendanceRepository) studentHolder(ctx context.Context, institutionID, where string, arg string, on time.Time) (domain.CardHolder, string, error) {
	h := domain.CardHolder{Kind: "student"}
	var status string
	err := r.c.pool.QueryRow(ctx, `SELECT s.id, `+studentName()+`, s.code, s.status,
			coalesce((SELECT `+sectionLabel+` FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
				JOIN academic.sections sec ON sec.id = e.section_id JOIN academic.levels l ON l.id = sec.level_id LEFT JOIN academic.grades g ON g.id = sec.grade_id
				WHERE e.student_id = s.id AND e.status = 'active' AND $3::date BETWEEN p.starts_on AND p.ends_on ORDER BY p.starts_on DESC LIMIT 1), ''),
			coalesce((SELECT sec.shift FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
				JOIN academic.sections sec ON sec.id = e.section_id
				WHERE e.student_id = s.id AND e.status = 'active' AND $3::date BETWEEN p.starts_on AND p.ends_on ORDER BY p.starts_on DESC LIMIT 1), '')
		FROM people.students s WHERE s.institution_id = $1 AND `+where, institutionID, arg, date(on)).
		Scan(&h.ID, &h.Name, &h.Code, &status, &h.Detail, &h.Shift)
	return h, status, academicErr(err)
}

func (r *AttendanceRepository) staffHolder(ctx context.Context, institutionID, where string, arg string) (domain.CardHolder, string, error) {
	h := domain.CardHolder{Kind: "staff"}
	var status string
	err := r.c.pool.QueryRow(ctx, `SELECT st.id, st.given_names || ' ' || st.surnames, st.code, st.status, st.position
		FROM people.staff st WHERE st.institution_id = $1 AND `+where, institutionID, arg).
		Scan(&h.ID, &h.Name, &h.Code, &status, &h.Detail)
	return h, status, academicErr(err)
}

func (r *AttendanceRepository) HolderByToken(ctx context.Context, institutionID, kind, token string, on time.Time) (domain.CardHolder, string, error) {
	if kind == "staff" {
		return r.staffHolder(ctx, institutionID, `st.card_token = $2`, token)
	}
	return r.studentHolder(ctx, institutionID, `s.card_token = $2`, token, on)
}

func (r *AttendanceRepository) HolderByCode(ctx context.Context, institutionID, code string, on time.Time) (domain.CardHolder, string, error) {
	h, status, err := r.studentHolder(ctx, institutionID, `(upper(s.code) = upper($2) OR s.document_number = $2)`, code, on)
	if !errors.Is(err, domain.ErrNotFound) {
		return h, status, err
	}
	return r.staffHolder(ctx, institutionID, `(upper(st.code) = upper($2) OR st.document_number = $2)`, code)
}

func (r *AttendanceRepository) SectionOn(ctx context.Context, institutionID, studentID string, on time.Time) (string, string, error) {
	var id, shift string
	err := r.c.pool.QueryRow(ctx, `SELECT sec.id, sec.shift FROM enrollment.enrollments e
		JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
		JOIN academic.sections sec ON sec.id = e.section_id
		WHERE e.institution_id = $1 AND e.student_id = $2 AND e.status = 'active' AND $3::date BETWEEN p.starts_on AND p.ends_on
		ORDER BY p.starts_on DESC LIMIT 1`, institutionID, studentID, date(on)).Scan(&id, &shift)
	return id, shift, academicErr(err)
}

func cardTable(kind string) string {
	if kind == "staff" {
		return "people.staff"
	}
	return "people.students"
}

func (r *AttendanceRepository) CardTokens(ctx context.Context, institutionID, kind string, ids []string) (map[string]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, card_token FROM `+cardTable(kind)+` WHERE institution_id = $1 AND id = ANY($2::uuid[])`, institutionID, ids)
	if err != nil {
		return nil, academicErr(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, token string
		if err := rows.Scan(&id, &token); err != nil {
			return nil, err
		}
		out[id] = token
	}
	return out, academicErr(rows.Err())
}

func (r *AttendanceRepository) RenewCardToken(ctx context.Context, institutionID, kind, id string) (string, error) {
	var token string
	err := r.c.pool.QueryRow(ctx, `UPDATE `+cardTable(kind)+` SET card_token = replace(gen_random_uuid()::text, '-', '')
		WHERE institution_id = $1 AND id = $2 RETURNING card_token`, institutionID, id).Scan(&token)
	return token, academicErr(err)
}

// ---------- gate ----------

func (r *AttendanceRepository) RecordScan(ctx context.Context, institutionID string, h domain.CardHolder, at time.Time, source, userID string) error {
	var student, staff *string
	if h.Kind == "staff" {
		staff = &h.ID
	} else {
		student = &h.ID
	}
	_, err := r.c.pool.Exec(ctx, `INSERT INTO attendance.gate_scans (institution_id, student_id, staff_id, scanned_at, source, recorded_by)
		VALUES ($1, $2, $3, $4, $5, nullif($6, '')::uuid)`, institutionID, student, staff, at, source, userID)
	return academicErr(err)
}

func (r *AttendanceRepository) RecentScans(ctx context.Context, institutionID string, since time.Time, limit int) ([]domain.GateScan, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT gs.id, gs.scanned_at, gs.source,
			CASE WHEN gs.student_id IS NOT NULL THEN 'student' ELSE 'staff' END,
			coalesce(gs.student_id, gs.staff_id),
			coalesce(`+studentName()+`, st.given_names || ' ' || st.surnames),
			coalesce(s.code, st.code),
			coalesce(st.position, ''),
			coalesce(dm.status, sm.status, '')
		FROM attendance.gate_scans gs
		LEFT JOIN people.students s ON s.id = gs.student_id
		LEFT JOIN people.staff st ON st.id = gs.staff_id
		LEFT JOIN attendance.daily_marks dm ON dm.student_id = gs.student_id AND dm.day = (gs.scanned_at AT TIME ZONE 'America/Lima')::date
		LEFT JOIN attendance.staff_marks sm ON sm.staff_id = gs.staff_id AND sm.day = (gs.scanned_at AT TIME ZONE 'America/Lima')::date
		WHERE gs.institution_id = $1 AND gs.scanned_at >= $2
		ORDER BY gs.scanned_at DESC LIMIT $3`, institutionID, since, limit)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.GateScan, error) {
		var g domain.GateScan
		err := row.Scan(&g.ID, &g.ScannedAt, &g.Source, &g.Holder.Kind, &g.Holder.ID, &g.Holder.Name, &g.Holder.Code, &g.Holder.Detail, &g.Status)
		return g, err
	})
}

// ---------- daily marks ----------

func markTable(kind string) (table, column string) {
	if kind == "staff" {
		return "attendance.staff_marks", "staff_id"
	}
	return "attendance.daily_marks", "student_id"
}

func (r *AttendanceRepository) DailyMark(ctx context.Context, institutionID, kind, personID string, day time.Time) (domain.DailyMark, error) {
	table, column := markTable(kind)
	m := domain.DailyMark{PersonID: personID}
	err := r.c.pool.QueryRow(ctx, `SELECT day, status, arrived_at, source, note FROM `+table+` WHERE institution_id = $1 AND `+column+` = $2 AND day = $3`,
		institutionID, personID, date(day)).Scan(&m.Day, &m.Status, &m.ArrivedAt, &m.Source, &m.Note)
	return m, academicErr(err)
}

func (r *AttendanceRepository) SaveDailyMark(ctx context.Context, institutionID, kind string, m domain.DailyMark, userID string) error {
	var err error
	if kind == "staff" {
		_, err = r.c.pool.Exec(ctx, `INSERT INTO attendance.staff_marks (institution_id, staff_id, day, status, arrived_at, source, note)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (staff_id, day) DO UPDATE SET status = EXCLUDED.status, arrived_at = EXCLUDED.arrived_at, source = EXCLUDED.source,
				note = EXCLUDED.note, updated_at = now()`, institutionID, m.PersonID, date(m.Day), m.Status, m.ArrivedAt, m.Source, m.Note)
	} else {
		_, err = r.c.pool.Exec(ctx, `INSERT INTO attendance.daily_marks (institution_id, student_id, day, status, arrived_at, source, note, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::uuid)
			ON CONFLICT (student_id, day) DO UPDATE SET status = EXCLUDED.status, arrived_at = EXCLUDED.arrived_at, source = EXCLUDED.source,
				note = EXCLUDED.note, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			institutionID, m.PersonID, date(m.Day), m.Status, m.ArrivedAt, m.Source, m.Note, userID)
	}
	return academicErr(err)
}

func (r *AttendanceRepository) SectionDays(ctx context.Context, institutionID string, day time.Time) ([]domain.SectionDay, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT sec.id, `+sectionLabel+`, l.name, sec.shift, coalesce(t.given_names || ' ' || t.surnames, ''),
			count(e.id), count(*) FILTER (WHERE dm.status = 'present'), count(*) FILTER (WHERE dm.status = 'late'),
			count(*) FILTER (WHERE dm.status = 'absent'), count(*) FILTER (WHERE dm.status = 'justified')
		FROM academic.sections sec
		JOIN academic.periods p ON p.id = sec.period_id
		JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id
		LEFT JOIN people.staff t ON t.id = sec.tutor_id
		LEFT JOIN enrollment.enrollments e ON e.section_id = sec.id AND e.status = 'active'
		LEFT JOIN attendance.daily_marks dm ON dm.student_id = e.student_id AND dm.day = $2
		WHERE p.institution_id = $1 AND p.status = 'active' AND $2::date BETWEEN p.starts_on AND p.ends_on
		GROUP BY sec.id, l.position, l.name, g.position, g.short_name, sec.name, sec.shift, t.given_names, t.surnames, p.starts_on
		ORDER BY p.starts_on, l.position, g.position NULLS FIRST, sec.name`, institutionID, date(day))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SectionDay, error) {
		var s domain.SectionDay
		err := row.Scan(&s.SectionID, &s.SectionLabel, &s.LevelName, &s.Shift, &s.TutorName, &s.Enrolled, &s.Present, &s.Late, &s.Absent, &s.Justified)
		return s, err
	})
}

func (r *AttendanceRepository) Roster(ctx context.Context, institutionID, sectionID string, day time.Time) ([]domain.RosterRow, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			coalesce(dm.status, ''), dm.arrived_at, coalesce(dm.source, ''), coalesce(dm.note, ''),
			EXISTS (SELECT 1 FROM attendance.justifications j WHERE j.student_id = s.id AND j.status = 'pending' AND $3::date BETWEEN j.from_day AND j.to_day)
		FROM enrollment.enrollments e
		JOIN people.students s ON s.id = e.student_id
		LEFT JOIN attendance.daily_marks dm ON dm.student_id = s.id AND dm.day = $3
		WHERE e.institution_id = $1 AND e.section_id = $2 AND e.status = 'active'
		ORDER BY s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, sectionID, date(day))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.RosterRow, error) {
		var x domain.RosterRow
		err := row.Scan(&x.StudentID, &x.Code, &x.Name, &x.Status, &x.ArrivedAt, &x.Source, &x.Note, &x.PendingJustification)
		return x, err
	})
}

func (r *AttendanceRepository) StaffDay(ctx context.Context, institutionID string, day time.Time) ([]domain.StaffRow, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT st.id, st.code, st.surnames || ', ' || st.given_names, st.position,
			coalesce(sm.status, ''), sm.arrived_at, coalesce(sm.source, ''), coalesce(sm.note, '')
		FROM people.staff st
		LEFT JOIN attendance.staff_marks sm ON sm.staff_id = st.id AND sm.day = $2
		WHERE st.institution_id = $1 AND st.status = 'active'
		ORDER BY st.surnames, st.given_names`, institutionID, date(day))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.StaffRow, error) {
		var x domain.StaffRow
		err := row.Scan(&x.StaffID, &x.Code, &x.Name, &x.Position, &x.Status, &x.ArrivedAt, &x.Source, &x.Note)
		return x, err
	})
}

func (r *AttendanceRepository) CloseDay(ctx context.Context, institutionID string, day time.Time, sectionIDs []string, userID string) (int, error) {
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO attendance.daily_marks (institution_id, student_id, day, status, source, note, updated_by)
		SELECT $1, e.student_id, $2,
			CASE WHEN EXISTS (SELECT 1 FROM attendance.justifications j WHERE j.student_id = e.student_id AND j.status = 'approved' AND $2::date BETWEEN j.from_day AND j.to_day)
				THEN 'justified' ELSE 'absent' END,
			'closing', '', nullif($4, '')::uuid
		FROM enrollment.enrollments e
		WHERE e.institution_id = $1 AND e.status = 'active' AND e.section_id IN (`+sectionsOn+`)
			AND (coalesce(cardinality($3::uuid[]), 0) = 0 OR e.section_id = ANY($3::uuid[]))
		ON CONFLICT (student_id, day) DO NOTHING`, institutionID, date(day), sectionIDs, userID)
	if err != nil {
		return 0, academicErr(err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *AttendanceRepository) CloseStaffDay(ctx context.Context, institutionID string, day time.Time) (int, error) {
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO attendance.staff_marks (institution_id, staff_id, day, status, source)
		SELECT $1, st.id, $2, 'absent', 'closing' FROM people.staff st WHERE st.institution_id = $1 AND st.status = 'active'
		ON CONFLICT (staff_id, day) DO NOTHING`, institutionID, date(day))
	if err != nil {
		return 0, academicErr(err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *AttendanceRepository) TutorSections(ctx context.Context, institutionID, staffID string) ([]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT sec.id FROM academic.sections sec JOIN academic.periods p ON p.id = sec.period_id
		WHERE p.institution_id = $1 AND p.status = 'active' AND sec.tutor_id = $2`, institutionID, staffID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---------- classes ----------

func (r *AttendanceRepository) Sessions(ctx context.Context, institutionID string, day time.Time, assignmentIDs []string) (map[string]domain.ClassSlot, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT cs.id, cs.assignment_id, cs.block_id, cs.topic,
			(SELECT count(*) FROM attendance.class_marks m WHERE m.session_id = cs.id AND m.status = 'absent'),
			(SELECT count(*) FROM attendance.class_marks m WHERE m.session_id = cs.id)
		FROM attendance.class_sessions cs WHERE cs.institution_id = $1 AND cs.day = $2 AND cs.assignment_id = ANY($3::uuid[])`,
		institutionID, date(day), assignmentIDs)
	if err != nil {
		return nil, academicErr(err)
	}
	defer rows.Close()
	out := map[string]domain.ClassSlot{}
	for rows.Next() {
		var s domain.ClassSlot
		if err := rows.Scan(&s.SessionID, &s.AssignmentID, &s.BlockID, &s.Topic, &s.Absent, &s.Students); err != nil {
			return nil, err
		}
		out[s.AssignmentID+"|"+s.BlockID] = s
	}
	return out, academicErr(rows.Err())
}

func (r *AttendanceRepository) ClassRoster(ctx context.Context, institutionID, sectionID, subjectID, sessionID string, day time.Time) ([]domain.ClassMark, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			coalesce(m.status, ''), coalesce(m.note, ''), coalesce(dm.status, '')
		FROM enrollment.enrollments e
		JOIN people.students s ON s.id = e.student_id
		LEFT JOIN attendance.class_marks m ON m.session_id = nullif($4, '')::uuid AND m.student_id = s.id
		LEFT JOIN attendance.daily_marks dm ON dm.student_id = s.id AND dm.day = $5
		WHERE e.institution_id = $1 AND e.section_id = $2 AND e.status = 'active'
			AND (NOT EXISTS (SELECT 1 FROM enrollment.unit_enrollments u WHERE u.enrollment_id = e.id)
				OR EXISTS (SELECT 1 FROM enrollment.unit_enrollments u WHERE u.enrollment_id = e.id AND u.subject_id = $3))
		ORDER BY s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, sectionID, subjectID, sessionID, date(day))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ClassMark, error) {
		var x domain.ClassMark
		err := row.Scan(&x.StudentID, &x.Code, &x.Name, &x.Status, &x.Note, &x.DayStatus)
		return x, err
	})
}

func (r *AttendanceRepository) SaveSession(ctx context.Context, institutionID string, s domain.ClassSession, marks []domain.ClassMark) (string, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO attendance.class_sessions (institution_id, assignment_id, section_id, day, block_id, topic, taken_by)
		VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid)
		ON CONFLICT (assignment_id, day, block_id) DO UPDATE SET topic = EXCLUDED.topic, taken_by = EXCLUDED.taken_by, taken_at = now()
		RETURNING id`, institutionID, s.AssignmentID, s.SectionID, date(s.Day), s.BlockID, s.Topic, s.TakenBy).Scan(&id)
	if err != nil {
		return "", academicErr(err)
	}
	batch := &pgx.Batch{}
	for _, m := range marks {
		batch.Queue(`INSERT INTO attendance.class_marks (session_id, student_id, status, note) VALUES ($1, $2, $3, $4)
			ON CONFLICT (session_id, student_id) DO UPDATE SET status = EXCLUDED.status, note = EXCLUDED.note`, id, m.StudentID, m.Status, m.Note)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return "", academicErr(err)
	}
	return id, tx.Commit(ctx)
}

// ---------- justifications ----------

const justificationColumns = `j.id, j.student_id, ` + studentNameJ + `,
	coalesce((SELECT ` + sectionLabel + ` FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id AND p.status = 'active'
		JOIN academic.sections sec ON sec.id = e.section_id JOIN academic.levels l ON l.id = sec.level_id LEFT JOIN academic.grades g ON g.id = sec.grade_id
		WHERE e.student_id = j.student_id AND e.status = 'active' ORDER BY p.starts_on DESC LIMIT 1), ''),
	j.from_day, j.to_day, j.reason, j.description, j.status, j.submitted_name, j.submitted_at, j.reviewed_name, j.reviewed_at, j.review_note,
	(SELECT count(*) FROM attendance.daily_marks dm WHERE dm.student_id = j.student_id AND dm.day BETWEEN j.from_day AND j.to_day AND dm.status IN ('absent', 'justified'))`

const studentNameJ = `(SELECT s.given_names || ' ' || s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') FROM people.students s WHERE s.id = j.student_id)`

func scanJustification(row pgx.Row, extra ...any) (domain.Justification, error) {
	var j domain.Justification
	err := row.Scan(append([]any{&j.ID, &j.StudentID, &j.StudentName, &j.SectionLabel, &j.FromDay, &j.ToDay, &j.Reason, &j.Description, &j.Status,
		&j.SubmittedName, &j.SubmittedAt, &j.ReviewedName, &j.ReviewedAt, &j.ReviewNote, &j.AbsentDays}, extra...)...)
	return j, err
}

func (r *AttendanceRepository) CreateJustification(ctx context.Context, institutionID string, j domain.Justification, userID string) (domain.Justification, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO attendance.justifications (institution_id, student_id, from_day, to_day, reason, description, submitted_by, submitted_name)
		VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid, $8) RETURNING id`,
		institutionID, j.StudentID, date(j.FromDay), date(j.ToDay), j.Reason, j.Description, userID, j.SubmittedName).Scan(&id)
	if err != nil {
		return domain.Justification{}, academicErr(err)
	}
	return r.Justification(ctx, institutionID, id)
}

func (r *AttendanceRepository) Justifications(ctx context.Context, f domain.JustificationFilter) ([]domain.Justification, int, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+justificationColumns+`, count(*) OVER ()
		FROM attendance.justifications j
		WHERE j.institution_id = $1 AND ($2 = '' OR j.status = $2) AND ($3 = '' OR j.student_id = nullif($3, '')::uuid)
			AND (coalesce(cardinality($4::uuid[]), 0) = 0 OR EXISTS (SELECT 1 FROM enrollment.enrollments e WHERE e.student_id = j.student_id AND e.status = 'active' AND e.section_id = ANY($4::uuid[])))
		ORDER BY (j.status = 'pending') DESC, j.submitted_at DESC
		LIMIT $5 OFFSET $6`, f.InstitutionID, f.Status, f.StudentID, f.SectionIDs, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, academicErr(err)
	}
	defer rows.Close()
	var out []domain.Justification
	total := 0
	for rows.Next() {
		j, err := scanJustification(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, j)
	}
	return out, total, academicErr(rows.Err())
}

func (r *AttendanceRepository) Justification(ctx context.Context, institutionID, id string) (domain.Justification, error) {
	j, err := scanJustification(r.c.pool.QueryRow(ctx, `SELECT `+justificationColumns+` FROM attendance.justifications j WHERE j.institution_id = $1 AND j.id = $2`, institutionID, id))
	return j, academicErr(err)
}

func (r *AttendanceRepository) ReviewJustification(ctx context.Context, institutionID, id, status, note, userID, userName string) (domain.Justification, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return domain.Justification{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var studentID string
	var from, to time.Time
	err = tx.QueryRow(ctx, `UPDATE attendance.justifications SET status = $3, review_note = $4, reviewed_by = nullif($5, '')::uuid, reviewed_name = $6, reviewed_at = now()
		WHERE institution_id = $1 AND id = $2 AND status = 'pending' RETURNING student_id, from_day, to_day`,
		institutionID, id, status, note, userID, userName).Scan(&studentID, &from, &to)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, e := r.Justification(ctx, institutionID, id); e != nil {
			return domain.Justification{}, e
		}
		return domain.Justification{}, domain.ErrJustificationClosed
	}
	if err != nil {
		return domain.Justification{}, academicErr(err)
	}
	if status == domain.JustificationApproved {
		if _, err := tx.Exec(ctx, `UPDATE attendance.daily_marks SET status = 'justified', source = 'justification', updated_by = nullif($5, '')::uuid, updated_at = now()
			WHERE institution_id = $1 AND student_id = $2 AND day BETWEEN $3 AND $4 AND status = 'absent'`, institutionID, studentID, from, to, userID); err != nil {
			return domain.Justification{}, academicErr(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE attendance.class_marks m SET status = 'justified' FROM attendance.class_sessions cs
			WHERE cs.id = m.session_id AND cs.institution_id = $1 AND m.student_id = $2 AND cs.day BETWEEN $3 AND $4 AND m.status = 'absent'`,
			institutionID, studentID, from, to); err != nil {
			return domain.Justification{}, academicErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Justification{}, err
	}
	return r.Justification(ctx, institutionID, id)
}

// ---------- reports ----------

func (r *AttendanceRepository) StudentTallies(ctx context.Context, institutionID, periodID, sectionID string) ([]domain.StudentTally, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			sec.id, `+sectionLabel+`,
			count(*) FILTER (WHERE dm.status = 'present'), count(*) FILTER (WHERE dm.status = 'late'),
			count(*) FILTER (WHERE dm.status = 'absent'), count(*) FILTER (WHERE dm.status = 'justified')
		FROM enrollment.enrollments e
		JOIN academic.periods p ON p.id = e.period_id
		JOIN people.students s ON s.id = e.student_id
		JOIN academic.sections sec ON sec.id = e.section_id
		JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id
		LEFT JOIN attendance.daily_marks dm ON dm.student_id = s.id AND dm.day BETWEEN p.starts_on AND p.ends_on
		WHERE e.institution_id = $1 AND e.period_id = $2 AND e.status = 'active' AND ($3 = '' OR e.section_id = nullif($3, '')::uuid)
		GROUP BY s.id, s.code, s.paternal_surname, s.maternal_surname, s.given_names, sec.id, l.position, l.name, g.position, g.short_name, sec.name
		ORDER BY l.position, g.position NULLS FIRST, sec.name, s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, periodID, sectionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.StudentTally, error) {
		var t domain.StudentTally
		err := row.Scan(&t.StudentID, &t.Code, &t.Name, &t.SectionID, &t.SectionLabel, &t.Days.Present, &t.Days.Late, &t.Days.Absent, &t.Days.Justified)
		return t, err
	})
}

func (r *AttendanceRepository) SubjectTallies(ctx context.Context, institutionID, periodID, sectionID, studentID string) ([]domain.SubjectTally, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT m.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			`+sectionLabel+`, a.id, sub.name,
			count(*) FILTER (WHERE m.status = 'present'), count(*) FILTER (WHERE m.status = 'late'),
			count(*) FILTER (WHERE m.status = 'absent'), count(*) FILTER (WHERE m.status = 'justified')
		FROM attendance.class_marks m
		JOIN attendance.class_sessions cs ON cs.id = m.session_id
		JOIN academic.assignments a ON a.id = cs.assignment_id
		JOIN academic.subjects sub ON sub.id = a.subject_id
		JOIN people.students s ON s.id = m.student_id
		JOIN academic.sections sec ON sec.id = a.section_id
		JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id
		WHERE cs.institution_id = $1 AND a.period_id = $2 AND ($3 = '' OR a.section_id = nullif($3, '')::uuid) AND ($4 = '' OR m.student_id = nullif($4, '')::uuid)
		GROUP BY m.student_id, s.paternal_surname, s.maternal_surname, s.given_names, l.name, g.short_name, sec.name, a.id, sub.name, sub.position
		ORDER BY s.paternal_surname, s.maternal_surname, s.given_names, sub.position`, institutionID, periodID, sectionID, studentID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SubjectTally, error) {
		var t domain.SubjectTally
		err := row.Scan(&t.StudentID, &t.StudentName, &t.SectionLabel, &t.AssignmentID, &t.SubjectName, &t.Classes.Present, &t.Classes.Late, &t.Classes.Absent, &t.Classes.Justified)
		return t, err
	})
}

func (r *AttendanceRepository) DailyMarks(ctx context.Context, institutionID, studentID string, from, to time.Time) ([]domain.DailyMark, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT student_id, day, status, arrived_at, source, note FROM attendance.daily_marks
		WHERE institution_id = $1 AND student_id = $2 AND day BETWEEN $3 AND $4 ORDER BY day DESC`, institutionID, studentID, date(from), date(to))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DailyMark, error) {
		var m domain.DailyMark
		err := row.Scan(&m.PersonID, &m.Day, &m.Status, &m.ArrivedAt, &m.Source, &m.Note)
		return m, err
	})
}

func (r *AttendanceRepository) StudentPeriod(ctx context.Context, institutionID, studentID string) (string, string, string, string, error) {
	var period, section, label, name string
	err := r.c.pool.QueryRow(ctx, `SELECT p.id, coalesce(sec.id::text, ''), coalesce(`+sectionLabel+`, ''), `+studentName()+`
		FROM enrollment.enrollments e
		JOIN people.students s ON s.id = e.student_id
		JOIN academic.periods p ON p.id = e.period_id
		LEFT JOIN academic.sections sec ON sec.id = e.section_id
		LEFT JOIN academic.levels l ON l.id = sec.level_id
		LEFT JOIN academic.grades g ON g.id = sec.grade_id
		WHERE e.institution_id = $1 AND e.student_id = $2
		ORDER BY (p.status = 'active' AND e.status = 'active') DESC, p.starts_on DESC LIMIT 1`, institutionID, studentID).Scan(&period, &section, &label, &name)
	return period, section, label, name, academicErr(err)
}

func (r *AttendanceRepository) StudentsForEmail(ctx context.Context, institutionID, email string) ([]domain.CardHolder, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id, `+studentName()+`, s.code FROM people.students s
		WHERE s.institution_id = $1 AND s.status = 'active' AND (lower(s.email) = lower($2) OR EXISTS (
			SELECT 1 FROM people.student_guardians sg JOIN people.guardians gu ON gu.id = sg.guardian_id WHERE sg.student_id = s.id AND lower(gu.email) = lower($2)))
		ORDER BY s.given_names`, institutionID, email)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.CardHolder, error) {
		h := domain.CardHolder{Kind: "student"}
		err := row.Scan(&h.ID, &h.Name, &h.Code)
		return h, err
	})
}

// ---------- demo seed ----------

var _ port.AttendanceSeeder = (*AttendanceRepository)(nil)

func (r *AttendanceRepository) SeedRoster(ctx context.Context, institutionID, periodID string) ([]port.SeedStudent, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.student_id, sec.id, sec.shift,
			coalesce((SELECT gu.given_names || ' ' || gu.surnames FROM people.student_guardians sg JOIN people.guardians gu ON gu.id = sg.guardian_id
				WHERE sg.student_id = e.student_id ORDER BY sg.is_primary DESC LIMIT 1), '')
		FROM enrollment.enrollments e JOIN academic.sections sec ON sec.id = e.section_id
		WHERE e.institution_id = $1 AND e.period_id = $2 AND e.status = 'active' ORDER BY e.student_id`, institutionID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (port.SeedStudent, error) {
		var s port.SeedStudent
		err := row.Scan(&s.StudentID, &s.SectionID, &s.Shift, &s.GuardianName)
		return s, err
	})
}

func (r *AttendanceRepository) bulk(ctx context.Context, institutionID, table, column string, marks []domain.DailyMark) (int, error) {
	rows := make([][]any, len(marks))
	for i, m := range marks {
		rows[i] = []any{institutionID, m.PersonID, date(m.Day), m.Status, m.ArrivedAt, m.Source, m.Note}
	}
	n, err := r.c.pool.CopyFrom(ctx, pgx.Identifier{"attendance", table}, []string{"institution_id", column, "day", "status", "arrived_at", "source", "note"}, pgx.CopyFromRows(rows))
	return int(n), academicErr(err)
}

func (r *AttendanceRepository) BulkDailyMarks(ctx context.Context, institutionID string, marks []domain.DailyMark) (int, error) {
	return r.bulk(ctx, institutionID, "daily_marks", "student_id", marks)
}

func (r *AttendanceRepository) BulkStaffMarks(ctx context.Context, institutionID string, marks []domain.DailyMark) (int, error) {
	return r.bulk(ctx, institutionID, "staff_marks", "staff_id", marks)
}

func (r *AttendanceRepository) HasAttendance(ctx context.Context, institutionID string) (bool, error) {
	var any bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM attendance.daily_marks WHERE institution_id = $1)`, institutionID).Scan(&any)
	return any, academicErr(err)
}
