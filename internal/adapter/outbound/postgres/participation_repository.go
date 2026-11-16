package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ParticipationRepository implements port.ParticipationRepository on attendance.activity_* and the permanence tables
type ParticipationRepository struct{ c *Client }

func NewParticipationRepository(c *Client) *ParticipationRepository {
	return &ParticipationRepository{c: c}
}

var _ port.ParticipationRepository = (*ParticipationRepository)(nil)

const partStudentName = `st.paternal_surname || CASE WHEN st.maternal_surname <> '' THEN ' ' || st.maternal_surname ELSE '' END || ', ' || st.given_names`

func (r *ParticipationRepository) InstitutionBySlug(ctx context.Context, slug string) (domain.Institution, error) {
	var i domain.Institution
	err := r.c.pool.QueryRow(ctx, `SELECT id, slug, name, kind FROM iam.institutions WHERE slug = $1 AND is_active`, slug).Scan(&i.ID, &i.Slug, &i.Name, &i.Kind)
	return i, notFound(err)
}

func (r *ParticipationRepository) StaffIDByEmail(ctx context.Context, institutionID, email string) (string, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `SELECT id::text FROM people.staff WHERE institution_id = $1 AND lower(email) = lower($2) AND status = 'active' LIMIT 1`,
		institutionID, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, academicErr(err)
}

func (r *ParticipationRepository) RunningPeriod(ctx context.Context, institutionID string) (string, time.Time, time.Time, error) {
	var id string
	var from, to time.Time
	err := r.c.pool.QueryRow(ctx, `SELECT id::text, starts_on, ends_on FROM academic.periods WHERE institution_id = $1 AND status = 'active'
		ORDER BY starts_on DESC LIMIT 1`, institutionID).Scan(&id, &from, &to)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", from, to, nil
	}
	return id, from, to, academicErr(err)
}

func (r *ParticipationRepository) Period(ctx context.Context, institutionID, periodID string) (string, time.Time, time.Time, error) {
	var name string
	var from, to time.Time
	err := r.c.pool.QueryRow(ctx, `SELECT name, starts_on, ends_on FROM academic.periods WHERE institution_id = $1 AND id = $2`, institutionID, periodID).Scan(&name, &from, &to)
	return name, from, to, academicErr(err)
}

func (r *ParticipationRepository) Rule(ctx context.Context, institutionID string) (domain.PermanenceRule, error) {
	rule := domain.DefaultPermanenceRule
	var w, m, i, l int16
	var at time.Time
	err := r.c.pool.QueryRow(ctx, `SELECT window_days, min_active_days, inactive_days, low_percent, updated_by_name, updated_at
		FROM attendance.permanence_rules WHERE institution_id = $1`, institutionID).Scan(&w, &m, &i, &l, &rule.UpdatedByName, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return rule, nil
	}
	if err != nil {
		return rule, academicErr(err)
	}
	rule.WindowDays, rule.MinActiveDays, rule.InactiveDays, rule.LowPercent, rule.UpdatedAt = int(w), int(m), int(i), int(l), &at
	return rule, nil
}

func (r *ParticipationRepository) SaveRule(ctx context.Context, institutionID string, x domain.PermanenceRule) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO attendance.permanence_rules (institution_id, window_days, min_active_days, inactive_days, low_percent, updated_by_name)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (institution_id) DO UPDATE SET window_days = EXCLUDED.window_days, min_active_days = EXCLUDED.min_active_days,
		inactive_days = EXCLUDED.inactive_days, low_percent = EXCLUDED.low_percent, updated_by_name = EXCLUDED.updated_by_name`,
		institutionID, x.WindowDays, x.MinActiveDays, x.InactiveDays, x.LowPercent, x.UpdatedByName)
	return academicErr(err)
}

func (r *ParticipationRepository) DistanceSections(ctx context.Context, institutionID, periodID string) ([]domain.DistanceSection, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT s.id::text, s.name, l.name, coalesce(g.name, ''), coalesce(s.distance_variant, ''),
		coalesce((SELECT t.given_names || ' ' || t.surnames FROM people.staff t WHERE t.id = s.tutor_id), ''),
		(SELECT count(*) FROM enrollment.enrollments e WHERE e.section_id = s.id AND e.status = 'active')
		FROM academic.sections s JOIN academic.periods p ON p.id = s.period_id JOIN academic.levels l ON l.id = s.level_id
		LEFT JOIN academic.grades g ON g.id = s.grade_id
		WHERE p.institution_id = $1 AND s.period_id = $2 AND s.attention_mode = 'distance'
		ORDER BY l.position, g.position, s.name`, institutionID, periodID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DistanceSection, error) {
		var x domain.DistanceSection
		var n int64
		err := row.Scan(&x.ID, &x.Name, &x.LevelName, &x.GradeName, &x.Variant, &x.TutorName, &n)
		x.Students = int(n)
		return x, err
	})
	if out == nil {
		out = []domain.DistanceSection{}
	}
	return out, academicErr(err)
}

func (r *ParticipationRepository) Section(ctx context.Context, institutionID, sectionID string) (string, string, string, error) {
	var period, label, tutor, mode string
	err := r.c.pool.QueryRow(ctx, `SELECT s.period_id::text, coalesce(g.name || ' ', '') || s.name || ' · ' || l.name, coalesce(s.tutor_id::text, ''), coalesce(s.attention_mode, '')
		FROM academic.sections s JOIN academic.periods p ON p.id = s.period_id JOIN academic.levels l ON l.id = s.level_id
		LEFT JOIN academic.grades g ON g.id = s.grade_id
		WHERE p.institution_id = $1 AND s.id = $2`, institutionID, sectionID).Scan(&period, &label, &tutor, &mode)
	if err != nil {
		return "", "", "", academicErr(err)
	}
	if mode != "distance" {
		return "", "", "", domain.ErrNotDistanceSection
	}
	return period, label, tutor, nil
}

func (r *ParticipationRepository) SectionStudents(ctx context.Context, institutionID, sectionID string) ([]domain.PermanenceStudent, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.id::text, st.id::text, `+partStudentName+`, st.code, e.status, e.enrolled_on, e.ended_on
		FROM enrollment.enrollments e JOIN people.students st ON st.id = e.student_id
		WHERE e.institution_id = $1 AND e.section_id = $2
		ORDER BY e.status <> 'active', st.paternal_surname, st.maternal_surname, st.given_names`, institutionID, sectionID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PermanenceStudent, error) {
		var x domain.PermanenceStudent
		err := row.Scan(&x.EnrollmentID, &x.StudentID, &x.StudentName, &x.StudentCode, &x.Status, &x.EnrolledOn, &x.EndedOn)
		return x, err
	})
	if out == nil {
		out = []domain.PermanenceStudent{}
	}
	return out, academicErr(err)
}

func (r *ParticipationRepository) Activity(ctx context.Context, studentIDs []string, from, to time.Time) ([]domain.StudentActivity, error) {
	out := []domain.StudentActivity{}
	if len(studentIDs) == 0 {
		return out, nil
	}
	rows, err := r.c.pool.Query(ctx, `SELECT student_id::text, array_agg(DISTINCT day ORDER BY day), sum(logins), sum(views), sum(live), sum(attempts),
		sum(submissions), sum(posts), sum(tutoring), max(last_at)
		FROM attendance.activity_days WHERE student_id = ANY($1::uuid[]) AND day BETWEEN $2 AND $3
		GROUP BY student_id`, studentIDs, date(from), date(to))
	if err != nil {
		return nil, academicErr(err)
	}
	out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.StudentActivity, error) {
		var x domain.StudentActivity
		var l, v, lv, a, s, p, t int64
		var last time.Time
		err := row.Scan(&x.StudentID, &x.Days, &l, &v, &lv, &a, &s, &p, &t, &last)
		x.Logins, x.Views, x.Live, x.Attempts, x.Submissions, x.Posts, x.Tutoring, x.LastAt = int(l), int(v), int(lv), int(a), int(s), int(p), int(t), &last
		return x, err
	})
	return out, academicErr(err)
}

func (r *ParticipationRepository) Adjustments(ctx context.Context, enrollmentIDs []string) ([]domain.PermanenceAdjustment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT enrollment_id::text, window_start, present, reason, by_name FROM attendance.permanence_adjustments
		WHERE enrollment_id = ANY($1::uuid[]) ORDER BY window_start`, enrollmentIDs)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PermanenceAdjustment, error) {
		var x domain.PermanenceAdjustment
		err := row.Scan(&x.EnrollmentID, &x.WindowStart, &x.Present, &x.Reason, &x.ByName)
		return x, err
	})
	return out, academicErr(err)
}

func (r *ParticipationRepository) SaveAdjustment(ctx context.Context, institutionID string, a domain.PermanenceAdjustment, remove bool) error {
	if remove {
		_, err := r.c.pool.Exec(ctx, `DELETE FROM attendance.permanence_adjustments x USING enrollment.enrollments e
			WHERE e.id = x.enrollment_id AND e.institution_id = $1 AND x.enrollment_id = $2 AND x.window_start = $3`, institutionID, a.EnrollmentID, date(a.WindowStart))
		return academicErr(err)
	}
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO attendance.permanence_adjustments (enrollment_id, window_start, present, reason, by_name)
		SELECT e.id, $3, $4, $5, $6 FROM enrollment.enrollments e WHERE e.institution_id = $1 AND e.id = $2
		ON CONFLICT (enrollment_id, window_start) DO UPDATE SET present = EXCLUDED.present, reason = EXCLUDED.reason, by_name = EXCLUDED.by_name, created_at = now()`,
		institutionID, a.EnrollmentID, date(a.WindowStart), a.Present, a.Reason, a.ByName)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return academicErr(err)
}

func (r *ParticipationRepository) EnrollmentSection(ctx context.Context, institutionID, enrollmentID string) (string, string, error) {
	var section, name string
	err := r.c.pool.QueryRow(ctx, `SELECT coalesce(e.section_id::text, ''), `+partStudentName+` FROM enrollment.enrollments e JOIN people.students st ON st.id = e.student_id
		WHERE e.institution_id = $1 AND e.id = $2`, institutionID, enrollmentID).Scan(&section, &name)
	return section, name, academicErr(err)
}

func (r *ParticipationRepository) RoomParticipation(ctx context.Context, institutionID, roomID string) ([]domain.RoomParticipation, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT st.id::text, `+partStudentName+`, st.code,
		(SELECT count(DISTINCT ev.item_id) FROM attendance.activity_events ev WHERE ev.room_id = r.id AND ev.student_id = st.id AND ev.kind IN ('view', 'live')),
		coalesce(sum(d.views), 0), coalesce(sum(d.live), 0), coalesce(sum(d.attempts), 0), coalesce(sum(d.submissions), 0), coalesce(sum(d.posts), 0),
		count(d.day), max(d.last_at)
		FROM classroom.rooms r
		JOIN enrollment.enrollments e ON e.period_id = r.period_id AND e.status = 'active' AND e.grade_id IS NOT DISTINCT FROM r.grade_id
		JOIN academic.subjects sub ON sub.id = r.subject_id
		JOIN academic.curricula c ON c.id = sub.curriculum_id AND c.level_id = e.level_id
		JOIN people.students st ON st.id = e.student_id
		LEFT JOIN attendance.activity_days d ON d.student_id = st.id AND d.room_key = r.id
		WHERE r.institution_id = $1 AND r.id = $2
		GROUP BY r.id, st.id
		ORDER BY st.paternal_surname, st.maternal_surname, st.given_names`, institutionID, roomID)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.RoomParticipation, error) {
		var x domain.RoomParticipation
		var seen, v, l, a, s, p, days int64
		err := row.Scan(&x.StudentID, &x.StudentName, &x.StudentCode, &seen, &v, &l, &a, &s, &p, &days, &x.LastAt)
		x.ItemsSeen, x.Views, x.Live, x.Attempts, x.Submissions, x.Posts, x.ActiveDays = int(seen), int(v), int(l), int(a), int(s), int(p), int(days)
		return x, err
	})
	if out == nil {
		out = []domain.RoomParticipation{}
	}
	return out, academicErr(err)
}

func (r *ParticipationRepository) DropoutSignals(ctx context.Context, institutionID, periodID string, now time.Time) ([]domain.DropoutSignal, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT e.id::text, st.id::text, `+partStudentName+`, coalesce(g.short_name || ' ', '') || sec.name || ' · ' || l.name, e.enrolled_on,
		(SELECT max(d.last_at) FROM attendance.activity_days d WHERE d.student_id = st.id),
		(SELECT count(*) FROM classroom.rooms r
			JOIN academic.subjects sub ON sub.id = r.subject_id
			JOIN academic.curricula c ON c.id = sub.curriculum_id AND c.level_id = e.level_id
			JOIN classroom.items i ON i.room_id = r.id AND i.kind IN ('quiz', 'task') AND i.published
			JOIN classroom.units u ON u.id = i.unit_id AND u.published
			JOIN classroom.assessments a ON a.item_id = i.id AND a.closes_at < $3
			WHERE r.period_id = e.period_id AND r.grade_id IS NOT DISTINCT FROM e.grade_id AND r.status = 'published'
			  AND a.closes_at > e.enrolled_on
			  AND NOT EXISTS (SELECT 1 FROM classroom.submissions s WHERE s.item_id = i.id AND s.student_id = st.id AND s.submitted_at IS NOT NULL)
			  AND NOT EXISTS (SELECT 1 FROM classroom.extensions x WHERE x.item_id = i.id AND x.student_id = st.id AND x.closes_at >= $3))
		FROM enrollment.enrollments e
		JOIN academic.sections sec ON sec.id = e.section_id AND sec.attention_mode = 'distance'
		JOIN academic.levels l ON l.id = e.level_id
		LEFT JOIN academic.grades g ON g.id = e.grade_id
		JOIN people.students st ON st.id = e.student_id
		WHERE e.institution_id = $1 AND e.period_id = $2 AND e.status = 'active'
		ORDER BY st.paternal_surname, st.maternal_surname, st.given_names`, institutionID, periodID, now)
	if err != nil {
		return nil, academicErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.DropoutSignal, error) {
		var x domain.DropoutSignal
		var overdue int64
		err := row.Scan(&x.EnrollmentID, &x.StudentID, &x.StudentName, &x.Placement, &x.EnrolledOn, &x.LastAt, &overdue)
		x.Overdue = int(overdue)
		return x, err
	})
	if out == nil {
		out = []domain.DropoutSignal{}
	}
	return out, academicErr(err)
}
