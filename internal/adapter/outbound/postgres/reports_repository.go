package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"api/internal/core/domain"
	"api/internal/core/port"
)

// ReportsRepository implements port.ReportsRepository: read-only aggregates over the other schemas
type ReportsRepository struct{ c *Client }

func NewReportsRepository(c *Client) *ReportsRepository { return &ReportsRepository{c: c} }

var _ port.ReportsRepository = (*ReportsRepository)(nil)

// reportEnrolled is the CTE every aggregate starts from: the students enrolled in the period ($2) of the
// institution ($1), optionally in one level ($3), with the group they count in. Without a level the group is the
// level; with one it is the grade (or the section, for academies whose areas have no grades)
const reportEnrolled = `WITH en AS (
	SELECT e.student_id, e.status, e.kind,
		CASE WHEN $3 = '' THEN l.name ELSE coalesce(g.name, sec.name, l.name) END AS grp,
		CASE WHEN $3 = '' THEN l.position * 1000 ELSE l.position * 1000 + coalesce(g.position, 0) END AS gpos
	FROM enrollment.enrollments e
	JOIN academic.levels l ON l.id = e.level_id
	LEFT JOIN academic.grades g ON g.id = e.grade_id
	LEFT JOIN academic.sections sec ON sec.id = e.section_id
	WHERE e.institution_id = $1 AND e.period_id = $2 AND ($3 = '' OR e.level_id = nullif($3, '')::uuid))`

// reportCharges adds the period's charges that were not voided, with what valid payments covered of each
const reportCharges = reportEnrolled + `, ch AS (
	SELECT c.id, c.student_id, en.grp, en.gpos, c.amount - c.discount AS net, c.amount, c.discount, c.due_on,
		coalesce((SELECT sum(al.amount) FROM finance.allocations al JOIN finance.payments p ON p.id = al.payment_id
			WHERE al.charge_id = c.id AND p.status = 'valid'), 0) AS paid
	FROM finance.charges c JOIN en ON en.student_id = c.student_id
	WHERE c.institution_id = $1 AND c.period_id = $2 AND c.status <> 'void')`

const countsByStatus = `count(*) FILTER (WHERE dm.status = 'present'), count(*) FILTER (WHERE dm.status = 'late'),
	count(*) FILTER (WHERE dm.status = 'absent'), count(*) FILTER (WHERE dm.status = 'justified')`

const levelsMix = `count(*) FILTER (WHERE m.level = 'AD'), count(*) FILTER (WHERE m.level = 'A'),
	count(*) FILTER (WHERE m.level = 'B'), count(*) FILTER (WHERE m.level = 'C'), count(*)`

func base(f domain.ReportFilter) []any { return []any{f.InstitutionID, f.PeriodID, f.LevelID} }

func (r *ReportsRepository) Levels(ctx context.Context, institutionID string) ([]port.ReportOption, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, name FROM academic.levels WHERE institution_id = $1 AND is_active ORDER BY position, name`, institutionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (port.ReportOption, error) {
		var o port.ReportOption
		return o, row.Scan(&o.ID, &o.Name)
	})
}

func (r *ReportsRepository) Enrollment(ctx context.Context, f domain.ReportFilter) (domain.EnrollmentStats, error) {
	var s domain.EnrollmentStats
	err := r.c.pool.QueryRow(ctx, reportEnrolled+` SELECT
		count(*) FILTER (WHERE status = 'active'), count(*) FILTER (WHERE status = 'withdrawn'),
		count(*) FILTER (WHERE status = 'transferred'), count(*) FILTER (WHERE status = 'completed'),
		count(*) FILTER (WHERE kind = 'new'), count(*) FILTER (WHERE kind = 'continuing'),
		count(*) FILTER (WHERE kind = 'transfer_in'), count(*) FILTER (WHERE kind = 're_entry'),
		(SELECT coalesce(sum(s.capacity), 0) FROM academic.sections s WHERE s.period_id = $2 AND ($3 = '' OR s.level_id = nullif($3, '')::uuid))
		FROM en`, base(f)...).Scan(&s.Active, &s.Withdrawn, &s.Transferred, &s.Completed, &s.New, &s.Continuing, &s.TransferIn, &s.ReEntry, &s.Capacity)
	if err != nil {
		return s, err
	}
	// Seats and students by group: the students still enrolled (or who finished) against the sections' capacity
	rows, err := r.c.pool.Query(ctx, reportEnrolled+`, seats AS (
		SELECT CASE WHEN $3 = '' THEN l.name ELSE coalesce(g.name, s.name) END AS grp,
			min(CASE WHEN $3 = '' THEN l.position * 1000 ELSE l.position * 1000 + coalesce(g.position, 0) END) AS gpos, sum(s.capacity) AS cap
		FROM academic.sections s JOIN academic.levels l ON l.id = s.level_id LEFT JOIN academic.grades g ON g.id = s.grade_id
		WHERE s.period_id = $2 AND ($3 = '' OR s.level_id = nullif($3, '')::uuid) GROUP BY 1),
	act AS (SELECT grp, min(gpos) AS gpos, count(*) AS n FROM en WHERE status IN ('active', 'completed') GROUP BY 1)
	SELECT coalesce(act.grp, seats.grp), coalesce(act.n, 0), coalesce(seats.cap, 0)
	FROM act FULL JOIN seats ON seats.grp = act.grp ORDER BY coalesce(act.gpos, seats.gpos), 1`, base(f)...)
	if err != nil {
		return s, err
	}
	s.Groups, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.GroupEnrollment, error) {
		var g domain.GroupEnrollment
		return g, row.Scan(&g.Label, &g.Active, &g.Capacity)
	})
	return s, err
}

func (r *ReportsRepository) AttendanceDays(ctx context.Context, f domain.ReportFilter) ([]domain.AttendanceDay, error) {
	rows, err := r.c.pool.Query(ctx, reportEnrolled+` SELECT dm.day, `+countsByStatus+`
		FROM attendance.daily_marks dm JOIN en ON en.student_id = dm.student_id
		WHERE dm.institution_id = $1 AND dm.day BETWEEN $4 AND $5 GROUP BY dm.day ORDER BY dm.day`, append(base(f), f.From, f.To)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AttendanceDay, error) {
		var d domain.AttendanceDay
		return d, row.Scan(&d.Day, &d.Present, &d.Late, &d.Absent, &d.Justified)
	})
}

func scanGroupAttendance(rows pgx.Rows, err error) ([]domain.GroupAttendance, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.GroupAttendance, error) {
		var g domain.GroupAttendance
		return g, row.Scan(&g.Label, &g.Present, &g.Late, &g.Absent, &g.Justified)
	})
}

func (r *ReportsRepository) AttendanceGroups(ctx context.Context, f domain.ReportFilter) ([]domain.GroupAttendance, error) {
	return scanGroupAttendance(r.c.pool.Query(ctx, reportEnrolled+` SELECT en.grp, `+countsByStatus+`
		FROM attendance.daily_marks dm JOIN en ON en.student_id = dm.student_id
		WHERE dm.institution_id = $1 AND dm.day BETWEEN $4 AND $5 GROUP BY en.grp ORDER BY min(en.gpos), 1`, append(base(f), f.From, f.To)...))
}

// AttendanceTimeline covers the whole period up to today: by term when the period has terms (days between terms
// are left out), by month otherwise
func (r *ReportsRepository) AttendanceTimeline(ctx context.Context, f domain.ReportFilter) ([]domain.GroupAttendance, error) {
	return scanGroupAttendance(r.c.pool.Query(ctx, reportEnrolled+`, terms AS (SELECT * FROM academic.terms WHERE period_id = $2)
		SELECT coalesce(t.name, to_char(dm.day, 'YYYY-MM')) AS label, `+countsByStatus+`
		FROM attendance.daily_marks dm JOIN en ON en.student_id = dm.student_id
		JOIN academic.periods p ON p.id = $2
		LEFT JOIN terms t ON dm.day BETWEEN t.starts_on AND t.ends_on
		WHERE dm.institution_id = $1 AND dm.day BETWEEN p.starts_on AND least(p.ends_on, $4)
			AND (t.id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM terms))
		GROUP BY 1 ORDER BY min(dm.day)`, append(base(f), f.Today)...))
}

func scanMix(rows pgx.Rows, err error, scan func(pgx.CollectableRow, *domain.ResultMix) error) ([]domain.ResultMix, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.ResultMix, error) {
		var m domain.ResultMix
		return m, scan(row, &m)
	})
}

func scanLevels(row pgx.CollectableRow, m *domain.ResultMix) error {
	return row.Scan(&m.Label, &m.AD, &m.A, &m.B, &m.C, &m.Count)
}

func scanAverage(row pgx.CollectableRow, m *domain.ResultMix) error {
	return row.Scan(&m.Label, &m.Average, &m.Max, &m.Approved, &m.Failed, &m.Count)
}

func (r *ReportsRepository) LiteralTerms(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, reportEnrolled+` SELECT t.name, `+levelsMix+`
		FROM assessment.competency_marks m JOIN en ON en.student_id = m.student_id JOIN academic.terms t ON t.id = m.term_id
		WHERE m.institution_id = $1 AND t.period_id = $2 AND m.level <> '' GROUP BY t.number, t.name ORDER BY t.number`, base(f)...)
	return scanMix(rows, err, scanLevels)
}

// LiteralSubjects groups by area; with f.Subject set, by the competencies of that area. Areas are matched by
// name, since each level has its own curriculum with the same CNEB areas
func (r *ReportsRepository) LiteralSubjects(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, reportEnrolled+`, marks AS (
		SELECT m.level, coalesce(s.name, 'Competencias transversales') AS area, coalesce(s.position, 999) AS apos, c.name AS competency, c.position AS cpos
		FROM assessment.competency_marks m JOIN en ON en.student_id = m.student_id JOIN academic.terms t ON t.id = m.term_id
		JOIN academic.competencies c ON c.id = m.competency_id LEFT JOIN academic.subjects s ON s.id = c.subject_id
		WHERE m.institution_id = $1 AND t.period_id = $2 AND m.level <> '')
	SELECT CASE WHEN $4 = '' THEN area ELSE competency END, `+levelsMix+`
	FROM marks m WHERE $4 = '' OR area = $4
	GROUP BY 1 ORDER BY min(CASE WHEN $4 = '' THEN apos ELSE cpos END), 1`, append(base(f), f.Subject)...)
	return scanMix(rows, err, scanLevels)
}

func (r *ReportsRepository) LiteralGroups(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, reportEnrolled+` SELECT en.grp, `+levelsMix+`
		FROM assessment.competency_marks m JOIN en ON en.student_id = m.student_id JOIN academic.terms t ON t.id = m.term_id
		WHERE m.institution_id = $1 AND t.period_id = $2 AND m.level <> '' GROUP BY en.grp ORDER BY min(en.gpos), 1`, base(f)...)
	return scanMix(rows, err, scanLevels)
}

// ConductTerms: the average 0–20 conduct mark by term and how many are below the passing mark ($4)
func (r *ReportsRepository) ConductTerms(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, reportEnrolled+` SELECT t.name, avg(m.score)::float8, max(m.score)::float8,
		count(*) FILTER (WHERE m.score >= $4), count(*) FILTER (WHERE m.score < $4), count(*)
		FROM assessment.conduct_marks m JOIN en ON en.student_id = m.student_id JOIN academic.terms t ON t.id = m.term_id
		WHERE m.institution_id = $1 AND t.period_id = $2 GROUP BY t.number, t.name ORDER BY t.number`, append(base(f), f.Passing)...)
	return scanMix(rows, err, scanAverage)
}

// unitAverages: each student's weighted average in each unit of the period (0–20, from the scores so far)
const unitAverages = reportEnrolled + `, ua AS (
	SELECT a.subject_id, en.grp, en.gpos, sc.student_id, sum(sc.score * e.weight)::float8 / sum(e.weight) AS avg
	FROM assessment.scores sc JOIN assessment.evaluations e ON e.id = sc.evaluation_id
	JOIN academic.assignments a ON a.id = e.assignment_id JOIN en ON en.student_id = sc.student_id
	WHERE e.institution_id = $1 AND a.period_id = $2 GROUP BY a.subject_id, en.grp, en.gpos, sc.student_id)`

const averageMix = `avg(ua.avg), max(ua.avg), count(*) FILTER (WHERE round(ua.avg) >= $4), count(*) FILTER (WHERE round(ua.avg) < $4), count(*)`

func (r *ReportsRepository) VigesimalSubjects(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, unitAverages+` SELECT s.name, `+averageMix+`
		FROM ua JOIN academic.subjects s ON s.id = ua.subject_id GROUP BY s.name ORDER BY min(s.position), 1`, append(base(f), f.Passing)...)
	return scanMix(rows, err, scanAverage)
}

func (r *ReportsRepository) VigesimalGroups(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, unitAverages+` SELECT ua.grp, `+averageMix+` FROM ua GROUP BY ua.grp ORDER BY min(ua.gpos), 1`, append(base(f), f.Passing)...)
	return scanMix(rows, err, scanAverage)
}

// MockSeries: the average and best score of each published mock exam, in the order they were taken
func (r *ReportsRepository) MockSeries(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, reportEnrolled+` SELECT m.name, avg(a.score)::float8, max(a.score)::float8, 0, 0, count(*)
		FROM exams.answer_sheets a JOIN exams.mock_exams m ON m.id = a.mock_exam_id JOIN en ON en.student_id = a.student_id
		WHERE m.institution_id = $1 AND m.period_id = $2 AND m.status = 'published'
		GROUP BY m.id, m.name, m.held_on ORDER BY m.held_on, m.name`, base(f)...)
	return scanMix(rows, err, scanAverage)
}

// MockCourses: the share of right answers in each course across the published mock exams (0–100)
func (r *ReportsRepository) MockCourses(ctx context.Context, f domain.ReportFilter) ([]domain.ResultMix, error) {
	rows, err := r.c.pool.Query(ctx, reportEnrolled+`, answers AS (
		SELECT mc.course_name, mc.position, (x.v->>'right')::float8 AS ok,
			(x.v->>'right')::float8 + (x.v->>'wrong')::float8 + (x.v->>'blank')::float8 AS asked
		FROM exams.answer_sheets a JOIN exams.mock_exams m ON m.id = a.mock_exam_id JOIN en ON en.student_id = a.student_id
		CROSS JOIN LATERAL jsonb_each(a.by_course) AS x(code, v)
		JOIN exams.mock_courses mc ON mc.mock_exam_id = m.id AND mc.course_code = x.code
		WHERE m.institution_id = $1 AND m.period_id = $2 AND m.status = 'published')
	SELECT course_name, coalesce(sum(ok) * 100 / nullif(sum(asked), 0), 0), 0, 0, 0, count(*)
	FROM answers GROUP BY course_name ORDER BY min(position), 1`, base(f)...)
	return scanMix(rows, err, scanAverage)
}

func (r *ReportsRepository) FinanceTotals(ctx context.Context, f domain.ReportFilter) (domain.FinanceTotals, error) {
	var t domain.FinanceTotals
	err := r.c.pool.QueryRow(ctx, reportCharges+` SELECT `+centsOf("coalesce(sum(amount), 0)")+`, `+centsOf("coalesce(sum(discount), 0)")+`, `+centsOf("coalesce(sum(paid), 0)")+`,
		`+centsOf("coalesce(sum(net - paid) FILTER (WHERE due_on < $4), 0)")+`,
		count(DISTINCT student_id) FILTER (WHERE due_on < $4 AND net - paid > 0), count(DISTINCT student_id) FROM ch`, append(base(f), f.Today)...).
		Scan(&t.Expected, &t.Discounts, &t.Collected, &t.Overdue, &t.Debtors, &t.Students)
	return t, err
}

// FinanceMonths: by due month what fell due and how much of it is paid, and by payment month what came in
func (r *ReportsRepository) FinanceMonths(ctx context.Context, f domain.ReportFilter) ([]domain.MoneyFlow, error) {
	rows, err := r.c.pool.Query(ctx, reportCharges+`,
	due AS (SELECT to_char(due_on, 'YYYY-MM') AS m, sum(net) AS e, sum(paid) AS c FROM ch GROUP BY 1),
	rec AS (SELECT to_char(p.paid_on, 'YYYY-MM') AS m, sum(al.amount) AS r FROM finance.allocations al
		JOIN finance.payments p ON p.id = al.payment_id JOIN ch ON ch.id = al.charge_id WHERE p.status = 'valid' GROUP BY 1)
	SELECT coalesce(due.m, rec.m), `+centsOf("coalesce(due.e, 0)")+`, `+centsOf("coalesce(due.c, 0)")+`, `+centsOf("coalesce(rec.r, 0)")+`
	FROM due FULL JOIN rec ON rec.m = due.m ORDER BY 1`, base(f)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MoneyFlow, error) {
		var m domain.MoneyFlow
		return m, row.Scan(&m.Month, &m.Expected, &m.Collected, &m.Received)
	})
}

func (r *ReportsRepository) FinanceMethods(ctx context.Context, f domain.ReportFilter) ([]domain.MethodTotal, error) {
	rows, err := r.c.pool.Query(ctx, reportCharges+` SELECT p.method, `+centsOf("sum(al.amount)")+`, count(DISTINCT p.id)
		FROM finance.allocations al JOIN finance.payments p ON p.id = al.payment_id JOIN ch ON ch.id = al.charge_id
		WHERE p.status = 'valid' GROUP BY p.method ORDER BY 2 DESC`, base(f)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.MethodTotal, error) {
		var m domain.MethodTotal
		return m, row.Scan(&m.Method, &m.Amount, &m.Count)
	})
}

// FinanceAging: what is overdue, by days since it fell due (every bucket, empty ones included)
func (r *ReportsRepository) FinanceAging(ctx context.Context, f domain.ReportFilter) ([]domain.AgingBucket, error) {
	rows, err := r.c.pool.Query(ctx, reportCharges+`,
	buckets(n, label, lo, hi) AS (VALUES (1, '1 a 30 días', 1, 30), (2, '31 a 60 días', 31, 60), (3, '61 a 90 días', 61, 90), (4, 'Más de 90 días', 91, 100000)),
	late AS (SELECT student_id, net - paid AS owed, $4::date - due_on AS days FROM ch WHERE due_on < $4 AND net - paid > 0)
	SELECT b.label, `+centsOf("coalesce(sum(late.owed), 0)")+`, count(DISTINCT late.student_id)
	FROM buckets b LEFT JOIN late ON late.days BETWEEN b.lo AND b.hi GROUP BY b.n, b.label ORDER BY b.n`, append(base(f), f.Today)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.AgingBucket, error) {
		var b domain.AgingBucket
		return b, row.Scan(&b.Label, &b.Amount, &b.Students)
	})
}

func (r *ReportsRepository) FinanceGroups(ctx context.Context, f domain.ReportFilter) ([]domain.GroupDebt, error) {
	rows, err := r.c.pool.Query(ctx, reportCharges+` SELECT grp, `+centsOf("sum(net)")+`, `+centsOf("sum(paid)")+`,
		`+centsOf("coalesce(sum(net - paid) FILTER (WHERE due_on < $4), 0)")+`, count(DISTINCT student_id) FILTER (WHERE due_on < $4 AND net - paid > 0)
		FROM ch GROUP BY grp ORDER BY min(gpos), 1`, append(base(f), f.Today)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.GroupDebt, error) {
		var g domain.GroupDebt
		return g, row.Scan(&g.Label, &g.Expected, &g.Collected, &g.Overdue, &g.Debtors)
	})
}

// PeriodKpis: per period, the students who stayed, the attendance rate, the achievement (literal: share of AD and
// A; 0–20: share of passing unit averages; score: the average of the published mock exams) and the share of what
// fell due by today that is paid
func (r *ReportsRepository) PeriodKpis(ctx context.Context, institutionID, scale string, passing int, today time.Time) ([]domain.PeriodKpi, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT p.id, p.name, p.status,
		(SELECT count(*) FROM enrollment.enrollments e WHERE e.period_id = p.id AND e.status IN ('active', 'completed')),
		(SELECT coalesce(count(*) FILTER (WHERE dm.status IN ('present', 'late')) * 100.0 / nullif(count(*), 0), -1)::float8
			FROM attendance.daily_marks dm JOIN enrollment.enrollments e ON e.student_id = dm.student_id AND e.period_id = p.id
			WHERE dm.institution_id = $1 AND dm.day BETWEEN p.starts_on AND p.ends_on),
		CASE $2
		WHEN 'literal' THEN (SELECT coalesce(count(*) FILTER (WHERE m.level IN ('AD', 'A')) * 100.0 / nullif(count(*), 0), -1)::float8
			FROM assessment.competency_marks m JOIN academic.terms t ON t.id = m.term_id WHERE t.period_id = p.id AND m.level <> '')
		WHEN 'vigesimal' THEN (SELECT coalesce(count(*) FILTER (WHERE round(ua.avg) >= $3) * 100.0 / nullif(count(*), 0), -1)::float8 FROM (
			SELECT sum(sc.score * e.weight)::float8 / sum(e.weight) AS avg FROM assessment.scores sc
			JOIN assessment.evaluations e ON e.id = sc.evaluation_id JOIN academic.assignments a ON a.id = e.assignment_id
			WHERE a.period_id = p.id GROUP BY a.id, sc.student_id) ua)
		ELSE (SELECT coalesce(avg(a.score), -1)::float8 FROM exams.answer_sheets a JOIN exams.mock_exams m ON m.id = a.mock_exam_id
			WHERE m.period_id = p.id AND m.status = 'published')
		END,
		(SELECT coalesce(sum(x.paid) * 100.0 / nullif(sum(x.net), 0), -1)::float8 FROM (
			SELECT c.amount - c.discount AS net, coalesce((SELECT sum(al.amount) FROM finance.allocations al JOIN finance.payments pay ON pay.id = al.payment_id
				WHERE al.charge_id = c.id AND pay.status = 'valid'), 0) AS paid
			FROM finance.charges c WHERE c.period_id = p.id AND c.status <> 'void' AND c.due_on <= $4) x)
	FROM academic.periods p WHERE p.institution_id = $1 AND p.status <> 'planning' ORDER BY p.starts_on, p.name`, institutionID, scale, passing, today)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.PeriodKpi, error) {
		var k domain.PeriodKpi
		return k, row.Scan(&k.PeriodID, &k.Name, &k.Status, &k.Students, &k.Attendance, &k.Achievement, &k.Collection)
	})
}
