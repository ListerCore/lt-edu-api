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

// FinanceRepository implements port.FinanceRepository. Amounts travel as céntimos (domain.Money)
type FinanceRepository struct{ c *Client }

func NewFinanceRepository(c *Client) *FinanceRepository { return &FinanceRepository{c: c} }

var _ port.FinanceRepository = (*FinanceRepository)(nil)

func financeErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.ConstraintName == "fee_plans_key" {
		return domain.Validation(map[string]string{"levelId": "Ya hay un plan para ese periodo y nivel."})
	}
	return academicErr(err)
}

// soles turns céntimos into the numeric the columns store
func soles(m domain.Money) string { return fmt.Sprintf("%d.%02d", int64(m)/100, int64(m)%100) }

const centsFormat = `round(%s * 100)::bigint`

func centsOf(col string) string { return fmt.Sprintf(centsFormat, col) }

func (r *FinanceRepository) Plans(ctx context.Context, institutionID, periodID string) ([]domain.FeePlan, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT p.id FROM finance.fee_plans p JOIN academic.periods pe ON pe.id = p.period_id LEFT JOIN academic.levels l ON l.id = p.level_id
		WHERE p.institution_id = $1 AND ($2 = '' OR p.period_id = nullif($2, '')::uuid) ORDER BY pe.starts_on DESC, l.position NULLS FIRST`, institutionID, periodID)
	if err != nil {
		return nil, financeErr(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make([]domain.FeePlan, 0, len(ids))
	for _, id := range ids {
		p, err := r.Plan(ctx, institutionID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *FinanceRepository) Plan(ctx context.Context, institutionID, id string) (domain.FeePlan, error) {
	var p domain.FeePlan
	err := r.c.pool.QueryRow(ctx, `SELECT p.id, p.period_id, pe.name, coalesce(p.level_id::text, ''), coalesce(l.name, ''), p.name,
			(SELECT count(DISTINCT ch.student_id) FROM finance.charges ch JOIN finance.fee_items i ON i.id = ch.item_id WHERE i.plan_id = p.id)
		FROM finance.fee_plans p JOIN academic.periods pe ON pe.id = p.period_id LEFT JOIN academic.levels l ON l.id = p.level_id
		WHERE p.institution_id = $1 AND p.id = $2`, institutionID, id).Scan(&p.ID, &p.PeriodID, &p.PeriodName, &p.LevelID, &p.LevelName, &p.Name, &p.Students)
	if err != nil {
		return p, financeErr(err)
	}
	rows, err := r.c.pool.Query(ctx, `SELECT id, concept, name, `+centsOf("amount")+`, due_on, position FROM finance.fee_items WHERE plan_id = $1 ORDER BY position, due_on`, id)
	if err != nil {
		return p, financeErr(err)
	}
	p.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.FeeItem, error) {
		var i domain.FeeItem
		err := row.Scan(&i.ID, &i.Concept, &i.Name, &i.Amount, &i.DueOn, &i.Position)
		return i, err
	})
	return p, err
}

func (r *FinanceRepository) SavePlan(ctx context.Context, institutionID string, p domain.FeePlan) (domain.FeePlan, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return domain.FeePlan{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id := p.ID
	if id == "" {
		err = tx.QueryRow(ctx, `INSERT INTO finance.fee_plans (institution_id, period_id, level_id, name) VALUES ($1, $2, nullif($3, '')::uuid, $4) RETURNING id`,
			institutionID, p.PeriodID, p.LevelID, p.Name).Scan(&id)
	} else {
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `UPDATE finance.fee_plans SET name = $3 WHERE institution_id = $1 AND id = $2`, institutionID, id, p.Name)
		if err == nil && tag.RowsAffected() == 0 {
			return domain.FeePlan{}, domain.ErrNotFound
		}
	}
	if err != nil {
		return domain.FeePlan{}, financeErr(err)
	}
	keep := []string{}
	for _, i := range p.Items {
		if i.ID != "" {
			keep = append(keep, i.ID)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM finance.fee_items WHERE plan_id = $1 AND NOT (id = ANY($2::uuid[]))`, id, keep); err != nil {
		return domain.FeePlan{}, financeErr(err)
	}
	for n, i := range p.Items {
		if i.ID == "" {
			_, err = tx.Exec(ctx, `INSERT INTO finance.fee_items (plan_id, concept, name, amount, due_on, position) VALUES ($1, $2, $3, $4::numeric, $5, $6)`,
				id, i.Concept, i.Name, soles(i.Amount), date(i.DueOn), n)
		} else {
			_, err = tx.Exec(ctx, `UPDATE finance.fee_items SET concept = $3, name = $4, amount = $5::numeric, due_on = $6, position = $7 WHERE plan_id = $1 AND id = $2`,
				id, i.ID, i.Concept, i.Name, soles(i.Amount), date(i.DueOn), n)
		}
		if err != nil {
			return domain.FeePlan{}, financeErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.FeePlan{}, err
	}
	return r.Plan(ctx, institutionID, id)
}

func (r *FinanceRepository) DeletePlan(ctx context.Context, institutionID, id string) error {
	tag, err := r.c.pool.Exec(ctx, `DELETE FROM finance.fee_plans WHERE institution_id = $1 AND id = $2`, institutionID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return financeErr(err)
}

func (r *FinanceRepository) GenerateCharges(ctx context.Context, institutionID, planID string) (int, error) {
	tag, err := r.c.pool.Exec(ctx, `INSERT INTO finance.charges (institution_id, student_id, period_id, item_id, concept, name, amount, discount, due_on)
		SELECT $1, e.student_id, p.period_id, i.id, i.concept, i.name, i.amount,
			coalesce(CASE WHEN d.id IS NOT NULL AND (NOT d.tuition_only OR i.concept = 'tuition') THEN round(i.amount * d.percent / 100, 2) END, 0),
			i.due_on
		FROM finance.fee_plans p
		JOIN finance.fee_items i ON i.plan_id = p.id
		JOIN enrollment.enrollments e ON e.period_id = p.period_id AND e.status = 'active' AND (p.level_id IS NULL OR e.level_id = p.level_id)
		LEFT JOIN finance.discounts d ON d.student_id = e.student_id AND d.period_id = p.period_id
		WHERE p.institution_id = $1 AND p.id = $2
			-- A student follows the plan of their level when there is one, the general plan otherwise
			AND (p.level_id IS NOT NULL OR NOT EXISTS (SELECT 1 FROM finance.fee_plans p2 WHERE p2.period_id = p.period_id AND p2.level_id = e.level_id))
		ON CONFLICT (student_id, item_id) DO NOTHING`, institutionID, planID)
	if err != nil {
		return 0, financeErr(err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *FinanceRepository) Discounts(ctx context.Context, institutionID, periodID, studentID string) ([]domain.Discount, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT d.id, d.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			d.period_id, d.percent::float8, d.tuition_only, d.reason
		FROM finance.discounts d JOIN people.students s ON s.id = d.student_id
		WHERE d.institution_id = $1 AND ($2 = '' OR d.period_id = nullif($2, '')::uuid) AND ($3 = '' OR d.student_id = nullif($3, '')::uuid)
		ORDER BY s.paternal_surname, s.given_names`, institutionID, periodID, studentID)
	if err != nil {
		return nil, financeErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Discount, error) {
		var d domain.Discount
		err := row.Scan(&d.ID, &d.StudentID, &d.StudentName, &d.PeriodID, &d.Percent, &d.TuitionOnly, &d.Reason)
		return d, err
	})
}

func (r *FinanceRepository) SaveDiscount(ctx context.Context, institutionID string, d domain.Discount) error {
	_, err := r.c.pool.Exec(ctx, `INSERT INTO finance.discounts (institution_id, student_id, period_id, percent, tuition_only, reason) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (student_id, period_id) DO UPDATE SET percent = EXCLUDED.percent, tuition_only = EXCLUDED.tuition_only, reason = EXCLUDED.reason`,
		institutionID, d.StudentID, d.PeriodID, d.Percent, d.TuitionOnly, d.Reason)
	return financeErr(err)
}

func (r *FinanceRepository) DeleteDiscount(ctx context.Context, institutionID, studentID, periodID string) error {
	_, err := r.c.pool.Exec(ctx, `DELETE FROM finance.discounts WHERE institution_id = $1 AND student_id = $2 AND period_id = $3`, institutionID, studentID, periodID)
	return financeErr(err)
}

func (r *FinanceRepository) ApplyDiscount(ctx context.Context, institutionID, studentID, periodID string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE finance.charges ch SET discount = coalesce((SELECT CASE WHEN NOT d.tuition_only OR ch.concept = 'tuition' THEN round(ch.amount * d.percent / 100, 2) END
			FROM finance.discounts d WHERE d.student_id = ch.student_id AND d.period_id = ch.period_id), 0)
		WHERE ch.institution_id = $1 AND ch.student_id = $2 AND ch.period_id = $3 AND ch.status = 'pending'
			AND NOT EXISTS (SELECT 1 FROM finance.allocations a JOIN finance.payments p ON p.id = a.payment_id WHERE a.charge_id = ch.id AND p.status = 'valid')`,
		institutionID, studentID, periodID)
	return financeErr(err)
}

const paidOf = `coalesce((SELECT sum(` + "a.amount" + `) FROM finance.allocations a JOIN finance.payments p ON p.id = a.payment_id WHERE a.charge_id = ch.id AND p.status = 'valid'), 0)`

const chargeColumns = `ch.id, ch.student_id, ch.period_id, pe.name, coalesce(ch.item_id::text, ''), ch.concept, ch.name, round(ch.amount * 100)::bigint, round(ch.discount * 100)::bigint,
	round(` + paidOf + ` * 100)::bigint, ch.due_on, ch.status, ch.void_reason`

func scanCharge(row pgx.Row) (domain.Charge, error) {
	var x domain.Charge
	err := row.Scan(&x.ID, &x.StudentID, &x.PeriodID, &x.PeriodName, &x.ItemID, &x.Concept, &x.Name, &x.Amount, &x.Discount, &x.Paid, &x.DueOn, &x.Status, &x.VoidReason)
	return x, err
}

func (r *FinanceRepository) Charges(ctx context.Context, institutionID, studentID string) ([]domain.Charge, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+chargeColumns+` FROM finance.charges ch JOIN academic.periods pe ON pe.id = ch.period_id
		WHERE ch.institution_id = $1 AND ch.student_id = $2 ORDER BY ch.due_on, ch.created_at`, institutionID, studentID)
	if err != nil {
		return nil, financeErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Charge, error) { return scanCharge(row) })
}

func (r *FinanceRepository) Charge(ctx context.Context, institutionID, id string) (domain.Charge, error) {
	x, err := scanCharge(r.c.pool.QueryRow(ctx, `SELECT `+chargeColumns+` FROM finance.charges ch JOIN academic.periods pe ON pe.id = ch.period_id WHERE ch.institution_id = $1 AND ch.id = $2`, institutionID, id))
	return x, financeErr(err)
}

func (r *FinanceRepository) VoidCharge(ctx context.Context, institutionID, id, reason string) error {
	_, err := r.c.pool.Exec(ctx, `UPDATE finance.charges SET status = 'void', void_reason = $3 WHERE institution_id = $1 AND id = $2`, institutionID, id, reason)
	return financeErr(err)
}

func (r *FinanceRepository) AddCharge(ctx context.Context, institutionID string, x domain.Charge) (domain.Charge, error) {
	var id string
	err := r.c.pool.QueryRow(ctx, `INSERT INTO finance.charges (institution_id, student_id, period_id, concept, name, amount, due_on) VALUES ($1, $2, $3, $4, $5, $6::numeric, $7) RETURNING id`,
		institutionID, x.StudentID, x.PeriodID, x.Concept, x.Name, soles(x.Amount), date(x.DueOn)).Scan(&id)
	if err != nil {
		return domain.Charge{}, financeErr(err)
	}
	return r.Charge(ctx, institutionID, id)
}

func (r *FinanceRepository) CreatePayment(ctx context.Context, institutionID string, p domain.Payment, userID string) (domain.Payment, error) {
	tx, err := r.c.pool.Begin(ctx)
	if err != nil {
		return domain.Payment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('receipt' || $1))`, institutionID); err != nil {
		return domain.Payment{}, err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO finance.payments (institution_id, student_id, number, receipt, paid_on, amount, method, reference, payer, notes, received_by, received_name)
		SELECT $1, $2, n, 'R001-' || lpad(n::text, 6, '0'), $3, $4::numeric, $5, $6, $7, $8, nullif($9, '')::uuid, $10
		FROM (SELECT coalesce(max(number), 0) + 1 AS n FROM finance.payments WHERE institution_id = $1) x RETURNING id`,
		institutionID, p.StudentID, date(p.PaidOn), soles(p.Amount), p.Method, p.Reference, p.Payer, p.Notes, userID, p.ReceivedName).Scan(&id)
	if err != nil {
		return domain.Payment{}, financeErr(err)
	}
	for _, a := range p.Allocations {
		if _, err := tx.Exec(ctx, `INSERT INTO finance.allocations (payment_id, charge_id, amount) VALUES ($1, $2, $3::numeric)`, id, a.ChargeID, soles(a.Amount)); err != nil {
			return domain.Payment{}, financeErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Payment{}, err
	}
	return r.Payment(ctx, institutionID, id)
}

const paymentColumns = `p.id, p.student_id, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names, s.code, p.number, p.receipt, p.paid_on,
	round(p.amount * 100)::bigint, p.method, p.reference, p.payer, p.notes, p.status, p.annul_reason, p.received_name`

func (r *FinanceRepository) scanPayments(ctx context.Context, rows pgx.Rows, err error) ([]domain.Payment, error) {
	if err != nil {
		return nil, financeErr(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Payment, error) {
		var p domain.Payment
		err := row.Scan(&p.ID, &p.StudentID, &p.StudentName, &p.StudentCode, &p.Number, &p.Receipt, &p.PaidOn, &p.Amount, &p.Method, &p.Reference, &p.Payer, &p.Notes, &p.Status, &p.AnnulReason, &p.ReceivedName)
		return p, err
	})
	if err != nil || len(out) == 0 {
		return out, err
	}
	ids := make([]string, len(out))
	index := map[string]int{}
	for i, p := range out {
		ids[i] = p.ID
		index[p.ID] = i
	}
	arows, err := r.c.pool.Query(ctx, `SELECT a.payment_id, a.charge_id, ch.name || ' · ' || pe.name, round(a.amount * 100)::bigint
		FROM finance.allocations a JOIN finance.charges ch ON ch.id = a.charge_id JOIN academic.periods pe ON pe.id = ch.period_id
		WHERE a.payment_id = ANY($1::uuid[]) ORDER BY ch.due_on`, ids)
	if err != nil {
		return nil, financeErr(err)
	}
	defer arows.Close()
	for arows.Next() {
		var pid string
		var a domain.Allocation
		if err := arows.Scan(&pid, &a.ChargeID, &a.ChargeName, &a.Amount); err != nil {
			return nil, err
		}
		out[index[pid]].Allocations = append(out[index[pid]].Allocations, a)
	}
	return out, financeErr(arows.Err())
}

func (r *FinanceRepository) Payment(ctx context.Context, institutionID, id string) (domain.Payment, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT `+paymentColumns+` FROM finance.payments p JOIN people.students s ON s.id = p.student_id WHERE p.institution_id = $1 AND p.id = $2`, institutionID, id)
	list, err := r.scanPayments(ctx, rows, err)
	if err != nil {
		return domain.Payment{}, err
	}
	if len(list) == 0 {
		return domain.Payment{}, domain.ErrNotFound
	}
	return list[0], nil
}

func (r *FinanceRepository) Payments(ctx context.Context, f domain.PaymentFilter) ([]domain.Payment, error) {
	var from, to *time.Time
	if !f.From.IsZero() {
		d := date(f.From)
		from = &d
	}
	if !f.To.IsZero() {
		d := date(f.To)
		to = &d
	}
	if f.Limit <= 0 {
		f.Limit = 200
	}
	rows, err := r.c.pool.Query(ctx, `SELECT `+paymentColumns+` FROM finance.payments p JOIN people.students s ON s.id = p.student_id
		WHERE p.institution_id = $1 AND ($2 = '' OR p.student_id = nullif($2, '')::uuid) AND ($3::date IS NULL OR p.paid_on >= $3) AND ($4::date IS NULL OR p.paid_on <= $4)
			AND ($5 = '' OR p.method = $5)
		ORDER BY p.paid_on DESC, p.number DESC LIMIT $6`, f.InstitutionID, f.StudentID, from, to, f.Method, f.Limit)
	return r.scanPayments(ctx, rows, err)
}

func (r *FinanceRepository) AnnulPayment(ctx context.Context, institutionID, id, reason string) error {
	tag, err := r.c.pool.Exec(ctx, `UPDATE finance.payments SET status = 'annulled', annul_reason = $3 WHERE institution_id = $1 AND id = $2 AND status = 'valid'`, institutionID, id, reason)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrPaymentAnnulled
	}
	return financeErr(err)
}

func (r *FinanceRepository) Accounts(ctx context.Context, institutionID, periodID string, today time.Time) ([]domain.Account, error) {
	rows, err := r.c.pool.Query(ctx, `WITH ch AS (
			SELECT ch.*, `+paidOf+` AS paid FROM finance.charges ch WHERE ch.institution_id = $1 AND ch.period_id = $2 AND ch.status = 'pending'
		)
		SELECT s.id, s.code, s.paternal_surname || coalesce(' ' || nullif(s.maternal_surname, ''), '') || ', ' || s.given_names,
			coalesce((SELECT coalesce(g.short_name || ' ', '') || coalesce(sec.name, '') || ' · ' || l.name FROM enrollment.enrollments e JOIN academic.levels l ON l.id = e.level_id
				LEFT JOIN academic.grades g ON g.id = e.grade_id LEFT JOIN academic.sections sec ON sec.id = e.section_id WHERE e.student_id = s.id AND e.period_id = $2 LIMIT 1), ''),
			coalesce((SELECT e.level_id::text FROM enrollment.enrollments e WHERE e.student_id = s.id AND e.period_id = $2 LIMIT 1), ''),
			round(sum(ch.amount) * 100)::bigint, round(sum(ch.discount) * 100)::bigint, round(sum(ch.paid) * 100)::bigint,
			round(coalesce(sum(ch.amount - ch.discount - ch.paid) FILTER (WHERE ch.due_on < $3), 0) * 100)::bigint,
			count(*) FILTER (WHERE ch.due_on < $3 AND ch.amount - ch.discount - ch.paid > 0),
			min(ch.due_on) FILTER (WHERE ch.due_on < $3 AND ch.amount - ch.discount - ch.paid > 0),
			(SELECT max(rm.sent_at) FROM finance.reminders rm WHERE rm.student_id = s.id)
		FROM ch JOIN people.students s ON s.id = ch.student_id
		GROUP BY s.id ORDER BY s.paternal_surname, s.maternal_surname, s.given_names`, institutionID, periodID, date(today))
	if err != nil {
		return nil, financeErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Account, error) {
		var a domain.Account
		err := row.Scan(&a.StudentID, &a.Code, &a.Name, &a.Placement, &a.LevelID, &a.Charged, &a.Discounts, &a.Paid, &a.Overdue, &a.OverdueCount, &a.OldestDue, &a.LastReminder)
		return a, err
	})
}

func (r *FinanceRepository) Reminders(ctx context.Context, institutionID, studentID string) ([]domain.Reminder, error) {
	rows, err := r.c.pool.Query(ctx, `SELECT id, student_id, message, round(overdue * 100)::bigint, sent_name, sent_at FROM finance.reminders
		WHERE institution_id = $1 AND student_id = $2 ORDER BY sent_at DESC LIMIT 20`, institutionID, studentID)
	if err != nil {
		return nil, financeErr(err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Reminder, error) {
		var x domain.Reminder
		err := row.Scan(&x.ID, &x.StudentID, &x.Message, &x.Overdue, &x.SentName, &x.SentAt)
		return x, err
	})
}

func (r *FinanceRepository) AddReminders(ctx context.Context, institutionID string, items []domain.Reminder) error {
	batch := &pgx.Batch{}
	for _, x := range items {
		batch.Queue(`INSERT INTO finance.reminders (institution_id, student_id, message, overdue, sent_name) VALUES ($1, $2, $3, $4::numeric, $5)`, institutionID, x.StudentID, x.Message, soles(x.Overdue), x.SentName)
	}
	return financeErr(r.c.pool.SendBatch(ctx, batch).Close())
}

func (r *FinanceRepository) StudentName(ctx context.Context, institutionID, studentID string) (string, string, error) {
	var name, code string
	err := r.c.pool.QueryRow(ctx, `SELECT given_names || ' ' || paternal_surname || coalesce(' ' || nullif(maternal_surname, ''), ''), code FROM people.students WHERE institution_id = $1 AND id = $2`,
		institutionID, studentID).Scan(&name, &code)
	return name, code, financeErr(err)
}

// ---------- demo seed ----------

func (r *FinanceRepository) HasFinance(ctx context.Context, institutionID string) (bool, error) {
	var any bool
	err := r.c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM finance.fee_plans WHERE institution_id = $1)`, institutionID).Scan(&any)
	return any, financeErr(err)
}

// SeedPayment stores a demo payment with a given date and receipt (no locking: the seed runs alone)
func (r *FinanceRepository) SeedPayment(ctx context.Context, institutionID string, p domain.Payment) error {
	_, err := r.CreatePayment(ctx, institutionID, p, "")
	return err
}
