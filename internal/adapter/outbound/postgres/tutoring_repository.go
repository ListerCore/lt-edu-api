package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// TutoringRepository is virtual tutoring (phase E6): tutors in force, sessions, messages and SAANEE referrals
type TutoringRepository struct{ c *Client }

func NewTutoringRepository(c *Client) *TutoringRepository { return &TutoringRepository{c: c} }

var _ port.TutoringRepository = (*TutoringRepository)(nil)

const staffName = `st.given_names || ' ' || st.surnames`

func (r *TutoringRepository) RunningPeriod(ctx context.Context, institutionID string) (string, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `SELECT id::text FROM academic.periods WHERE institution_id = $1 AND status = 'active' ORDER BY starts_on DESC LIMIT 1`, institutionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, academicErr(err)
}

// tutorOf is every active enrollment of the period with the tutor in force: the student's own, or the section's
const tutorOf = `SELECT e.student_id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names AS name,
		` + sectionLabel + ` AS label, sec.id AS section_id, coalesce(sec.attention_mode, '') AS mode,
		coalesce(ta.staff_id, sec.tutor_id) AS staff_id, ta.staff_id IS NOT NULL AS individual,
		(SELECT max(d.last_at) FROM attendance.activity_days d WHERE d.student_id = e.student_id) AS last_at
	FROM enrollment.enrollments e JOIN people.students s ON s.id = e.student_id
	JOIN academic.sections sec ON sec.id = e.section_id JOIN academic.levels l ON l.id = sec.level_id LEFT JOIN academic.grades g ON g.id = sec.grade_id
	LEFT JOIN wellbeing.tutor_assignments ta ON ta.period_id = e.period_id AND ta.student_id = e.student_id
	WHERE e.institution_id = $1 AND e.period_id = $2 AND e.status = 'active'`

func (r *TutoringRepository) TutorStudents(ctx context.Context, institutionID, periodID, staffID string) ([]domain.TutorStudent, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT t.student_id, t.code, t.name, t.label, t.section_id, t.mode, coalesce(t.staff_id::text, ''), coalesce(`+staffName+`, ''), t.individual, t.last_at
		FROM (`+tutorOf+`) t LEFT JOIN people.staff st ON st.id = t.staff_id
		WHERE $3 = '' OR t.staff_id = nullif($3, '')::uuid
		ORDER BY t.label, t.name`, institutionID, periodID, staffID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TutorStudent, error) {
		var x domain.TutorStudent
		err := row.Scan(&x.StudentID, &x.Code, &x.Name, &x.Placement, &x.SectionID, &x.AttentionMode, &x.StaffID, &x.StaffName, &x.Individual, &x.LastAt)
		return x, err
	})
}

func (r *TutoringRepository) TutorLoads(ctx context.Context, institutionID, periodID string) ([]domain.TutorLoad, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT t.staff_id::text, `+staffName+`,
			coalesce((SELECT array_agg(DISTINCT `+sectionLabel+`) FROM academic.sections sec JOIN academic.levels l ON l.id = sec.level_id
				LEFT JOIN academic.grades g ON g.id = sec.grade_id WHERE sec.period_id = $2 AND sec.tutor_id = t.staff_id), '{}'),
			count(*) FILTER (WHERE NOT t.individual), count(*) FILTER (WHERE t.individual)
		FROM (`+tutorOf+`) t JOIN people.staff st ON st.id = t.staff_id
		GROUP BY t.staff_id, st.given_names, st.surnames ORDER BY count(*) DESC, 2`, institutionID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TutorLoad, error) {
		var x domain.TutorLoad
		var bySection, individual int64
		err := row.Scan(&x.StaffID, &x.Name, &x.Sections, &bySection, &individual)
		x.BySection, x.Individual = int(bySection), int(individual)
		return x, err
	})
}

func (r *TutoringRepository) TutorCandidates(ctx context.Context, institutionID string) ([]domain.TutorCandidate, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT st.id::text, `+staffName+`, st.position FROM people.staff st
		WHERE st.institution_id = $1 AND st.status = 'active'
		  AND (st.is_teacher OR EXISTS (SELECT 1 FROM academic.sections sec WHERE sec.tutor_id = st.id))
		ORDER BY st.surnames, st.given_names`, institutionID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TutorCandidate, error) {
		var x domain.TutorCandidate
		err := row.Scan(&x.StaffID, &x.Name, &x.Position)
		return x, err
	})
}

func (r *TutoringRepository) AssignTutor(ctx context.Context, institutionID, periodID string, studentIDs []string, staffID, by string) (int, error) {
	if staffID == "" {
		tag, err := r.c.pool.Exec(ctx, `DELETE FROM wellbeing.tutor_assignments WHERE institution_id = $1 AND period_id = $2 AND student_id = ANY($3::uuid[])`,
			institutionID, periodID, studentIDs)
		return int(tag.RowsAffected()), academicErr(err)
	}
	// Only students enrolled in the period and staff of the institution
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO wellbeing.tutor_assignments (period_id, student_id, institution_id, staff_id, assigned_by_name)
		SELECT e.period_id, e.student_id, e.institution_id, st.id, $5 FROM enrollment.enrollments e
		JOIN people.staff st ON st.id = $4::uuid AND st.institution_id = e.institution_id
		WHERE e.institution_id = $1 AND e.period_id = $2 AND e.student_id = ANY($3::uuid[]) AND e.status = 'active'
		ON CONFLICT (period_id, student_id) DO UPDATE SET staff_id = EXCLUDED.staff_id, assigned_by_name = EXCLUDED.assigned_by_name, created_at = now()`,
		institutionID, periodID, studentIDs, staffID, by)
	return int(tag.RowsAffected()), academicErr(err)
}

func (r *TutoringRepository) IndividualTutees(ctx context.Context, institutionID, staffID string) ([]string, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT ta.student_id::text FROM wellbeing.tutor_assignments ta JOIN academic.periods p ON p.id = ta.period_id AND p.status = 'active'
		WHERE ta.institution_id = $1 AND ta.staff_id = $2`, institutionID, staffID)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ---------- sessions ----------

const sessionSelect = `SELECT ts.id, ts.staff_id, ` + staffName + `, ts.kind, ts.mode, ts.scheduled_at, ts.duration_min, ts.link, ts.reason, ts.alert_kind, ts.status,
	ts.notes, ts.agreements, ts.held_at, ts.created_by_name, ts.provider
	FROM wellbeing.tutoring_sessions ts JOIN people.staff st ON st.id = ts.staff_id`

func scanSession(row pgx.Row) (domain.TutoringSession, error) {
	var s domain.TutoringSession
	var duration int16
	err := row.Scan(&s.ID, &s.StaffID, &s.StaffName, &s.Kind, &s.Mode, &s.ScheduledAt, &duration, &s.Link, &s.Reason, &s.AlertKind, &s.Status,
		&s.Notes, &s.Agreements, &s.HeldAt, &s.CreatedName, &s.Provider)
	s.DurationMin = int(duration)
	return s, err
}

func (r *TutoringRepository) attendees(ctx context.Context, sessions []domain.TutoringSession) error {
	if len(sessions) == 0 {
		return nil
	}
	ids := make([]string, len(sessions))
	at := map[string]int{}
	for i, s := range sessions {
		ids[i], at[s.ID] = s.ID, i
	}
	rows, err := r.c.pool.Query(ctx, `SELECT a.session_id::text, a.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			`+placementOf("s.id")+`, a.attended, coalesce(a.interview_id::text, '')
		FROM wellbeing.tutoring_attendees a JOIN people.students s ON s.id = a.student_id
		WHERE a.session_id = ANY($1::uuid[]) ORDER BY s.paternal_surname, s.maternal_surname, s.given_names`, ids)
	if err != nil {
		return academicErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var a domain.SessionAttendee
		if err := rows.Scan(&sid, &a.StudentID, &a.StudentName, &a.Placement, &a.Attended, &a.InterviewID); err != nil {
			return err
		}
		sessions[at[sid]].Attendees = append(sessions[at[sid]].Attendees, a)
	}
	return rows.Err()
}

func (r *TutoringRepository) Sessions(ctx context.Context, f domain.SessionFilter) ([]domain.TutoringSession, error) {
	rows, err := r.c.pool.Query(ctx, sessionSelect+` WHERE ts.institution_id = $1 AND ($2 = '' OR ts.staff_id = nullif($2, '')::uuid)
		AND ($3 = '' OR EXISTS (SELECT 1 FROM wellbeing.tutoring_attendees a WHERE a.session_id = ts.id AND a.student_id = nullif($3, '')::uuid))
		AND ($4::timestamptz IS NULL OR ts.scheduled_at >= $4) AND ($5::timestamptz IS NULL OR ts.scheduled_at < $5)
		ORDER BY ts.scheduled_at`, f.InstitutionID, f.StaffID, f.StudentID, f.From, f.To)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.TutoringSession, error) { return scanSession(row) })
	if err != nil {
		return nil, academicErr(err)
	}
	return out, r.attendees(ctx, out)
}

func (r *TutoringRepository) Session(ctx context.Context, institutionID, id string) (domain.TutoringSession, error) {
	s, err := scanSession(r.c.pool.QueryRow(ctx, sessionSelect+` WHERE ts.institution_id = $1 AND ts.id = $2`, institutionID, id))
	if err != nil {
		return s, academicErr(err)
	}
	list := []domain.TutoringSession{s}
	err = r.attendees(ctx, list)
	return list[0], err
}

func (r *TutoringRepository) SaveSession(ctx context.Context, institutionID string, s domain.TutoringSession, studentIDs []string) (domain.TutoringSession, error) {
	id := s.ID
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if id == "" {
			if err := tx.QueryRow(ctx, `INSERT INTO wellbeing.tutoring_sessions (institution_id, staff_id, kind, mode, scheduled_at, duration_min, link, reason, alert_kind, created_by_name, provider)
				SELECT $1, st.id, $3, $4, $5, $6, $7, $8, $9, $10, $11 FROM people.staff st WHERE st.institution_id = $1 AND st.id = $2 RETURNING id`,
				institutionID, s.StaffID, s.Kind, s.Mode, s.ScheduledAt, s.DurationMin, s.Link, s.Reason, s.AlertKind, s.CreatedName, providerOrMeet(s.Provider)).Scan(&id); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `UPDATE wellbeing.tutoring_sessions SET kind = $3, mode = $4, scheduled_at = $5, duration_min = $6, link = $7, reason = $8, provider = $9, status = 'scheduled'
				WHERE institution_id = $1 AND id = $2 AND status IN ('scheduled', 'missed', 'cancelled')`, institutionID, id, s.Kind, s.Mode, s.ScheduledAt, s.DurationMin, s.Link, s.Reason, providerOrMeet(s.Provider))
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
			if _, err := tx.Exec(ctx, `DELETE FROM wellbeing.tutoring_attendees WHERE session_id = $1`, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO wellbeing.tutoring_attendees (session_id, student_id)
			SELECT $1, s.id FROM people.students s WHERE s.institution_id = $2 AND s.id = ANY($3::uuid[])`, id, institutionID, studentIDs)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TutoringSession{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.TutoringSession{}, academicErr(err)
	}
	return r.Session(ctx, institutionID, id)
}

func (r *TutoringRepository) HoldSession(ctx context.Context, institutionID string, in port.HoldInput) (domain.TutoringSession, error) {
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE wellbeing.tutoring_sessions SET status = 'held', held_at = $3, notes = $4, agreements = $5
			WHERE institution_id = $1 AND id = $2 AND status <> 'held'`, institutionID, in.SessionID, in.At, in.Notes, in.Agreements)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if _, err := tx.Exec(ctx, `UPDATE wellbeing.tutoring_attendees SET attended = student_id = ANY($2::uuid[]) WHERE session_id = $1`, in.SessionID, in.Attended); err != nil {
			return err
		}
		// One interview in the file of each student who came
		_, err = tx.Exec(ctx, `WITH made AS (
				INSERT INTO wellbeing.interviews (institution_id, student_id, kind, held_on, topic, notes, agreements, author_id, author_name)
				SELECT $1, a.student_id, 'student', $3, $4, $5, $6, nullif($7, '')::uuid, $8 FROM wellbeing.tutoring_attendees a
				WHERE a.session_id = $2 AND a.attended RETURNING id, student_id)
			UPDATE wellbeing.tutoring_attendees a SET interview_id = made.id FROM made WHERE a.session_id = $2 AND a.student_id = made.student_id`,
			institutionID, in.SessionID, in.HeldOn, in.Topic, in.Notes, in.Agreements, in.AuthorID, in.AuthorName)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TutoringSession{}, domain.ErrSessionClosed
	}
	if err != nil {
		return domain.TutoringSession{}, academicErr(err)
	}
	return r.Session(ctx, institutionID, in.SessionID)
}

func (r *TutoringRepository) SetSessionStatus(ctx context.Context, institutionID, id, status string) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE wellbeing.tutoring_sessions SET status = $3 WHERE institution_id = $1 AND id = $2 AND status <> 'held'`, institutionID, id, status)
	if err != nil {
		return academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrSessionClosed
	}
	return nil
}

// ---------- messages ----------

const threadSelect = `SELECT t.id, t.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, ` + "%s" + `,
	t.staff_id, ` + staffName + `, t.last_message_at,
	coalesce(lm.body, ''), coalesce(lm.side, ''),
	(SELECT count(*) FROM wellbeing.messages m WHERE m.thread_id = t.id AND m.side <> $4
		AND m.created_at > coalesce(CASE WHEN $4 = 'staff' THEN t.staff_read_at ELSE t.student_read_at END, '-infinity'))
	FROM wellbeing.threads t JOIN people.students s ON s.id = t.student_id JOIN people.staff st ON st.id = t.staff_id
	LEFT JOIN LATERAL (SELECT m.body, m.side FROM wellbeing.messages m WHERE m.thread_id = t.id ORDER BY m.created_at DESC LIMIT 1) lm ON true`

func scanThread(row pgx.Row) (domain.Thread, error) {
	var t domain.Thread
	var unread int64
	err := row.Scan(&t.ID, &t.StudentID, &t.StudentName, &t.Placement, &t.StaffID, &t.StaffName, &t.LastMessageAt, &t.LastBody, &t.LastSide, &unread)
	t.Unread = int(unread)
	return t, err
}

func threadQuery(where string) string {
	return fmt.Sprintf(threadSelect, placementOf("s.id")) + " " + where
}

func (r *TutoringRepository) Threads(ctx context.Context, institutionID, staffID, studentID string) ([]domain.Thread, error) {
	side := domain.SideStaff
	if staffID == "" {
		side = domain.SideStudent
	}
	rows, err := r.c.pool.Query(ctx, threadQuery(`WHERE t.institution_id = $1 AND ($2 = '' OR t.staff_id = nullif($2, '')::uuid)
		AND ($3 = '' OR t.student_id = nullif($3, '')::uuid) ORDER BY t.last_message_at DESC NULLS LAST`), institutionID, staffID, studentID, side)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Thread, error) { return scanThread(row) })
}

func (r *TutoringRepository) Thread(ctx context.Context, institutionID, id string) (domain.Thread, error) {
	t, err := scanThread(r.c.pool.QueryRow(ctx, threadQuery(`WHERE t.institution_id = $1 AND t.id = $2 AND $3::text = $3::text`), institutionID, id, "", domain.SideStaff))
	return t, academicErr(err)
}

func (r *TutoringRepository) EnsureThread(ctx context.Context, institutionID, studentID, staffID string) (domain.Thread, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.threads (institution_id, student_id, staff_id)
		SELECT $1, s.id, st.id FROM people.students s JOIN people.staff st ON st.id = $3 AND st.institution_id = $1 WHERE s.institution_id = $1 AND s.id = $2
		ON CONFLICT (student_id, staff_id) DO UPDATE SET student_id = EXCLUDED.student_id RETURNING id`, institutionID, studentID, staffID).Scan(&id)
	if err != nil {
		return domain.Thread{}, academicErr(err)
	}
	return r.Thread(ctx, institutionID, id)
}

func (r *TutoringRepository) Messages(ctx context.Context, threadID string, limit int) ([]domain.Message, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, thread_id, side, author_name, body, created_at FROM (
			SELECT * FROM wellbeing.messages WHERE thread_id = $1 ORDER BY created_at DESC LIMIT $2) m ORDER BY created_at`, threadID, limit)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Message, error) {
		var m domain.Message
		err := row.Scan(&m.ID, &m.ThreadID, &m.Side, &m.AuthorName, &m.Body, &m.CreatedAt)
		return m, err
	})
}

func (r *TutoringRepository) AddMessage(ctx context.Context, threadID string, m domain.Message, authorID string) (domain.Message, error) {
	err := pgx.BeginFunc(ctx, r.c.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO wellbeing.messages (thread_id, side, author_id, author_name, body) VALUES ($1, $2, nullif($3, '')::uuid, $4, $5)
			RETURNING id, thread_id, created_at`, threadID, m.Side, authorID, m.AuthorName, m.Body).Scan(&m.ID, &m.ThreadID, &m.CreatedAt); err != nil {
			return err
		}
		// Writing reads the thread
		_, err := tx.Exec(ctx, `UPDATE wellbeing.threads SET last_message_at = $2,
			staff_read_at = CASE WHEN $3 = 'staff' THEN $2 ELSE staff_read_at END, student_read_at = CASE WHEN $3 = 'student' THEN $2 ELSE student_read_at END
			WHERE id = $1`, threadID, m.CreatedAt, m.Side)
		return err
	})
	return m, academicErr(err)
}

func (r *TutoringRepository) MarkRead(ctx context.Context, threadID, side string, at time.Time) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE wellbeing.threads SET staff_read_at = CASE WHEN $2 = 'staff' THEN $3 ELSE staff_read_at END,
		student_read_at = CASE WHEN $2 = 'student' THEN $3 ELSE student_read_at END WHERE id = $1`, threadID, side, at)
	return academicErr(err)
}

// ---------- SAANEE ----------

const saaneeSelect = `SELECT x.id, x.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, %s,
	x.reason, x.status, x.sent_on, x.reference, x.response, x.referred_name, x.updated_name, x.updated_at
	FROM wellbeing.saanee_referrals x JOIN people.students s ON s.id = x.student_id`

func scanSaanee(row pgx.Row) (domain.SaaneeReferral, error) {
	var x domain.SaaneeReferral
	err := row.Scan(&x.ID, &x.StudentID, &x.StudentName, &x.Placement, &x.Reason, &x.Status, &x.SentOn, &x.Reference, &x.Response, &x.ReferredName, &x.UpdatedName, &x.UpdatedAt)
	return x, err
}

func (r *TutoringRepository) SaaneeReferrals(ctx context.Context, institutionID string, studentIDs []string) ([]domain.SaaneeReferral, error) {
	rows, err := r.c.pool.Query(ctx, fmt.Sprintf(saaneeSelect, placementOf("s.id"))+` WHERE x.institution_id = $1 AND ($2::uuid[] IS NULL OR x.student_id = ANY($2))
		ORDER BY x.status = 'closed', x.sent_on DESC`, institutionID, uuids(studentIDs))
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SaaneeReferral, error) { return scanSaanee(row) })
}

func (r *TutoringRepository) SaaneeReferral(ctx context.Context, institutionID, id string) (domain.SaaneeReferral, error) {
	x, err := scanSaanee(r.c.pool.QueryRow(ctx, fmt.Sprintf(saaneeSelect, placementOf("s.id"))+` WHERE x.institution_id = $1 AND x.id = $2`, institutionID, id))
	return x, academicErr(err)
}

func (r *TutoringRepository) CreateSaanee(ctx context.Context, institutionID string, x domain.SaaneeReferral) (domain.SaaneeReferral, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO wellbeing.saanee_referrals (institution_id, student_id, reason, sent_on, reference, referred_name, updated_name)
		SELECT $1, s.id, $3, $4, $5, $6, $6 FROM people.students s WHERE s.institution_id = $1 AND s.id = $2 RETURNING id`,
		institutionID, x.StudentID, x.Reason, x.SentOn, x.Reference, x.ReferredName).Scan(&id)
	if err != nil {
		return x, academicErr(err)
	}
	return r.SaaneeReferral(ctx, institutionID, id)
}

func (r *TutoringRepository) UpdateSaanee(ctx context.Context, institutionID string, x domain.SaaneeReferral) (domain.SaaneeReferral, error) {
	tag, err := r.c.pool.Exec(ctx, `UPDATE wellbeing.saanee_referrals SET status = $3, reference = $4, response = $5, updated_name = $6
		WHERE institution_id = $1 AND id = $2`, institutionID, x.ID, x.Status, x.Reference, x.Response, x.UpdatedName)
	if err != nil {
		return x, academicErr(err)
	}
	if tag.RowsAffected() == 0 {
		return x, domain.ErrNotFound
	}
	return r.SaaneeReferral(ctx, institutionID, x.ID)
}

// ---------- pending work ----------

func (r *TutoringRepository) PendingWork(ctx context.Context, institutionID, periodID string, studentIDs []string, from, to, now time.Time) ([]domain.PendingWork, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.student_id, i.id, r.id, i.title, sub.name, coalesce(x.closes_at, a.closes_at) AS due
		FROM enrollment.enrollments e
		JOIN classroom.rooms r ON r.period_id = e.period_id AND r.grade_id IS NOT DISTINCT FROM e.grade_id AND r.status = 'published'
		JOIN academic.subjects sub ON sub.id = r.subject_id
		JOIN academic.curricula c ON c.id = sub.curriculum_id AND c.level_id = e.level_id
		JOIN classroom.items i ON i.room_id = r.id AND i.kind IN ('quiz', 'task') AND i.published
		JOIN classroom.units u ON u.id = i.unit_id AND u.published
		JOIN classroom.assessments a ON a.item_id = i.id AND a.closes_at IS NOT NULL
		LEFT JOIN classroom.extensions x ON x.item_id = i.id AND x.student_id = e.student_id
		WHERE e.institution_id = $1 AND e.period_id = $2 AND e.status = 'active' AND e.student_id = ANY($3::uuid[])
		  AND coalesce(x.closes_at, a.closes_at) BETWEEN $4 AND $5 AND coalesce(x.closes_at, a.closes_at) > e.enrolled_on
		  AND NOT EXISTS (SELECT 1 FROM classroom.submissions s WHERE s.item_id = i.id AND s.student_id = e.student_id AND s.submitted_at IS NOT NULL)
		ORDER BY due`, institutionID, periodID, studentIDs, from, to)
	if err != nil {
		return nil, academicErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PendingWork, error) {
		var w domain.PendingWork
		err := row.Scan(&w.StudentID, &w.ItemID, &w.RoomID, &w.Title, &w.Subject, &w.ClosesAt)
		w.Overdue = w.ClosesAt.Before(now)
		return w, err
	})
}
