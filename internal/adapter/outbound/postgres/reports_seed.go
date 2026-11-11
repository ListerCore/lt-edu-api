package postgres

import "context"

// SeedAttendanceHistory fills the school days of every running or closed period from its start up to the first
// day already recorded (or its end), so the board shows the whole period and not only the seeded weeks. Days
// between terms are vacations; one student in nine misses more, and winter (June and July) a bit more for all
func (r *ReportsRepository) SeedAttendanceHistory(ctx context.Context, institutionID string) (int, error) {
	tag, err := r.c.pool.Exec(ctx, `WITH p AS (
		SELECT p.id, p.starts_on, EXISTS (SELECT 1 FROM academic.terms t WHERE t.period_id = p.id) AS has_terms,
			least(p.ends_on, current_date - 1, coalesce((SELECT min(dm.day) - 1 FROM attendance.daily_marks dm
				JOIN enrollment.enrollments e ON e.student_id = dm.student_id AND e.period_id = p.id WHERE dm.institution_id = $1), p.ends_on)) AS until
		FROM academic.periods p WHERE p.institution_id = $1 AND p.status <> 'planning'),
	days AS (
		SELECT p.id AS period_id, d::date AS day FROM p, generate_series(p.starts_on, p.until, interval '1 day') AS d
		WHERE extract(isodow FROM d) < 6
			AND (NOT p.has_terms OR EXISTS (SELECT 1 FROM academic.terms t WHERE t.period_id = p.id AND d::date BETWEEN t.starts_on AND t.ends_on))),
	draws AS (
		SELECT e.student_id, days.day, random() AS r,
			0.018 + CASE WHEN abs(hashtext(e.student_id::text)) % 9 = 0 THEN 0.05 ELSE 0 END
				+ CASE WHEN extract(month FROM days.day) IN (6, 7) THEN 0.025 ELSE 0 END AS miss
		FROM days JOIN enrollment.enrollments e ON e.period_id = days.period_id AND e.institution_id = $1
		WHERE days.day >= e.enrolled_on AND (e.status IN ('active', 'completed') OR days.day <= e.ended_on))
	INSERT INTO attendance.daily_marks (institution_id, student_id, day, status, source)
	SELECT $1, student_id, day,
		CASE WHEN r < miss THEN 'absent' WHEN r < miss + 0.012 THEN 'justified' WHEN r < miss + 0.06 THEN 'late' ELSE 'present' END, 'manual'
	FROM draws ON CONFLICT (student_id, day) DO NOTHING`, institutionID)
	return int(tag.RowsAffected()), err
}

// SeedClosedYear gives the closed periods that have no marks yet the AD/A/B/C levels of every competency of each
// student's grade and term (a little below the running year, improving term by term), the conduct marks when
// conduct applies, and the fees of the running year one year earlier and 5 % cheaper, nearly all of them paid
func (r *ReportsRepository) SeedClosedYear(ctx context.Context, institutionID string) (int, error) {
	total := 0
	tag, err := r.c.pool.Exec(ctx, `WITH closed AS (
		SELECT p.id FROM academic.periods p WHERE p.institution_id = $1 AND p.status = 'closed'
			AND NOT EXISTS (SELECT 1 FROM assessment.competency_marks m JOIN academic.terms t ON t.id = m.term_id WHERE t.period_id = p.id)),
	targets AS (
		SELECT e.student_id, e.section_id, e.grade_id, t.id AS term_id, t.number, (abs(hashtext(e.student_id::text)) % 100) / 100.0 AS ability
		FROM enrollment.enrollments e JOIN closed ON closed.id = e.period_id JOIN academic.terms t ON t.period_id = e.period_id
		WHERE e.section_id IS NOT NULL AND e.grade_id IS NOT NULL),
	comps AS (
		SELECT DISTINCT sg.grade_id, c.id AS competency_id FROM academic.subject_grades sg
		JOIN academic.competencies c ON c.subject_id = sg.subject_id AND c.kind = 'competency'
		WHERE sg.subject_id IN (SELECT a.subject_id FROM academic.assignments a WHERE a.institution_id = $1))
	INSERT INTO assessment.competency_marks (institution_id, section_id, term_id, student_id, competency_id, level)
	SELECT $1, t.section_id, t.term_id, t.student_id, c.competency_id,
		CASE WHEN d.x < 0.10 THEN 'AD' WHEN d.x < 0.55 THEN 'A' WHEN d.x < 0.88 THEN 'B' ELSE 'C' END
	FROM targets t JOIN comps c ON c.grade_id = t.grade_id
	CROSS JOIN LATERAL (SELECT random() * 0.75 + (1 - t.ability) * 0.3 - t.number * 0.02 + 0 * hashtext(c.competency_id::text) AS x) d`, institutionID)
	if err != nil {
		return total, err
	}
	total += int(tag.RowsAffected())

	// Conduct only where the running year already has it (Educación Básica Regular)
	tag, err = r.c.pool.Exec(ctx, `INSERT INTO assessment.conduct_marks (institution_id, section_id, term_id, student_id, score)
	SELECT $1, e.section_id, t.id, e.student_id,
		CASE WHEN d.r < 0.03 THEN 8 + floor(d.r * 100)::int ELSE 12 + floor(d.r * 8)::int END
	FROM enrollment.enrollments e JOIN academic.periods p ON p.id = e.period_id AND p.status = 'closed'
	JOIN academic.terms t ON t.period_id = p.id
	CROSS JOIN LATERAL (SELECT random() + 0 * t.number + 0 * hashtext(e.student_id::text) AS r) d
	WHERE e.institution_id = $1 AND e.section_id IS NOT NULL
		AND EXISTS (SELECT 1 FROM assessment.conduct_marks m WHERE m.institution_id = $1)
		AND NOT EXISTS (SELECT 1 FROM assessment.conduct_marks m WHERE m.term_id = t.id)`, institutionID)
	if err != nil {
		return total, err
	}
	total += int(tag.RowsAffected())

	// Fees: the latest charges by level and name are the template, moved back the years between both periods
	tag, err = r.c.pool.Exec(ctx, `WITH src AS (
		SELECT c.period_id, p.starts_on FROM finance.charges c JOIN academic.periods p ON p.id = c.period_id
		WHERE c.institution_id = $1 ORDER BY c.due_on DESC LIMIT 1),
	closed AS (
		SELECT p.id, extract(year FROM src.starts_on)::int - extract(year FROM p.starts_on)::int AS years
		FROM academic.periods p, src WHERE p.institution_id = $1 AND p.status = 'closed' AND p.starts_on < src.starts_on
			AND NOT EXISTS (SELECT 1 FROM finance.charges c WHERE c.period_id = p.id)),
	tpl AS (
		SELECT DISTINCT ON (e.level_id, c.name) e.level_id, c.concept, c.name, c.amount, c.due_on
		FROM finance.charges c JOIN src ON src.period_id = c.period_id
		JOIN enrollment.enrollments e ON e.student_id = c.student_id AND e.period_id = c.period_id
		WHERE c.status <> 'void' AND c.item_id IS NOT NULL ORDER BY e.level_id, c.name, c.amount DESC),
	ch AS (
		INSERT INTO finance.charges (institution_id, student_id, period_id, concept, name, amount, due_on)
		SELECT $1, e.student_id, e.period_id, tpl.concept, tpl.name, round(tpl.amount * 0.095) * 10, (tpl.due_on - make_interval(years => closed.years))::date
		FROM enrollment.enrollments e JOIN closed ON closed.id = e.period_id JOIN tpl ON tpl.level_id = e.level_id
		RETURNING id, student_id, amount, due_on),
	pick AS (SELECT ch.*, row_number() OVER (ORDER BY ch.due_on, ch.id) AS rn FROM ch WHERE random() < 0.985),
	start AS (SELECT coalesce(max(number), 0) AS n FROM finance.payments WHERE institution_id = $1),
	pay AS (
		INSERT INTO finance.payments (institution_id, student_id, number, receipt, paid_on, amount, method, payer, received_name)
		SELECT $1, pick.student_id, start.n + pick.rn, 'R001-' || lpad((start.n + pick.rn)::text, 6, '0'),
			pick.due_on - (random() * 12)::int, pick.amount,
			(ARRAY['cash', 'transfer', 'yape', 'plin', 'card', 'deposit'])[1 + floor(random() * 6)::int],
			'Familia de ' || split_part(s.given_names, ' ', 1), 'Tesorería'
		FROM pick CROSS JOIN start JOIN people.students s ON s.id = pick.student_id
		RETURNING id, number)
	INSERT INTO finance.allocations (payment_id, charge_id, amount)
	SELECT pay.id, pick.id, pick.amount FROM pay JOIN pick ON pick.rn = pay.number - (SELECT n FROM start)`, institutionID)
	if err != nil {
		return total, err
	}
	return total + int(tag.RowsAffected()), nil
}
