-- Fees and payments (phase 10 of docs/plan-academico.md): the fee plan of each period (and level), the schedule of
-- charges of each student with their discounts, payments with an internal receipt and how they settle the charges,
-- and payment reminders shown to families. No late fees: overdue charges only count the days late
CREATE SCHEMA IF NOT EXISTS finance;

-- What a period charges: one plan per period and level (level NULL: every level of the period)
CREATE TABLE finance.fee_plans (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    level_id uuid REFERENCES academic.levels (id) ON DELETE CASCADE,
    name varchar(120) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fee_plans_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT fee_plans_key UNIQUE NULLS NOT DISTINCT (period_id, level_id)
);

CREATE TABLE finance.fee_items (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    plan_id uuid NOT NULL REFERENCES finance.fee_plans (id) ON DELETE CASCADE,
    -- enrollment | tuition | other
    concept varchar(20) NOT NULL,
    name varchar(120) NOT NULL,
    amount numeric(10, 2) NOT NULL,
    due_on date NOT NULL,
    position smallint NOT NULL,
    CONSTRAINT fee_items_concept_check CHECK (concept IN ('enrollment', 'tuition', 'other')),
    CONSTRAINT fee_items_amount_check CHECK (amount > 0 AND amount <= 100000),
    CONSTRAINT fee_items_name_required CHECK (btrim(name) <> '')
);

CREATE INDEX idx_finance_fee_items_plan ON finance.fee_items (plan_id, position);

-- A student's discount in a period (scholarship, siblings…): a percentage of the tuition, or of every charge
CREATE TABLE finance.discounts (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    percent numeric(5, 2) NOT NULL,
    tuition_only boolean NOT NULL DEFAULT true,
    reason varchar(200) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT discounts_key UNIQUE (student_id, period_id),
    CONSTRAINT discounts_percent_check CHECK (percent > 0 AND percent <= 100),
    CONSTRAINT discounts_reason_required CHECK (btrim(reason) <> '')
);

-- One charge of a student's schedule
CREATE TABLE finance.charges (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    period_id uuid NOT NULL REFERENCES academic.periods (id) ON DELETE CASCADE,
    item_id uuid REFERENCES finance.fee_items (id) ON DELETE SET NULL,
    concept varchar(20) NOT NULL,
    name varchar(120) NOT NULL,
    amount numeric(10, 2) NOT NULL,
    discount numeric(10, 2) NOT NULL DEFAULT 0,
    due_on date NOT NULL,
    -- pending | void (cancelled by the institution, e.g. after a withdrawal)
    status varchar(20) NOT NULL DEFAULT 'pending',
    void_reason varchar(200) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT charges_item_key UNIQUE (student_id, item_id),
    CONSTRAINT charges_concept_check CHECK (concept IN ('enrollment', 'tuition', 'other')),
    CONSTRAINT charges_amount_check CHECK (amount > 0 AND discount >= 0 AND discount <= amount),
    CONSTRAINT charges_status_check CHECK (status IN ('pending', 'void'))
);

CREATE INDEX idx_finance_charges_student ON finance.charges (student_id, due_on);
CREATE INDEX idx_finance_charges_period ON finance.charges (institution_id, period_id, due_on);

-- A payment received, with its internal receipt number (not a SUNAT electronic invoice)
CREATE TABLE finance.payments (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    number integer NOT NULL,
    receipt varchar(20) NOT NULL,
    paid_on date NOT NULL,
    amount numeric(10, 2) NOT NULL,
    -- cash | transfer | yape | plin | card | deposit
    method varchar(20) NOT NULL,
    reference varchar(80) NOT NULL DEFAULT '',
    payer varchar(160) NOT NULL DEFAULT '',
    notes varchar(300) NOT NULL DEFAULT '',
    -- valid | annulled
    status varchar(20) NOT NULL DEFAULT 'valid',
    annul_reason varchar(200) NOT NULL DEFAULT '',
    received_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    received_name varchar(160) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payments_number_key UNIQUE (institution_id, number),
    CONSTRAINT payments_amount_check CHECK (amount > 0 AND amount <= 1000000),
    CONSTRAINT payments_method_check CHECK (method IN ('cash', 'transfer', 'yape', 'plin', 'card', 'deposit')),
    CONSTRAINT payments_status_check CHECK (status IN ('valid', 'annulled'))
);

CREATE INDEX idx_finance_payments_day ON finance.payments (institution_id, paid_on DESC);
CREATE INDEX idx_finance_payments_student ON finance.payments (student_id, paid_on DESC);

-- How much of a payment went to each charge
CREATE TABLE finance.allocations (
    payment_id uuid NOT NULL REFERENCES finance.payments (id) ON DELETE CASCADE,
    charge_id uuid NOT NULL REFERENCES finance.charges (id) ON DELETE CASCADE,
    amount numeric(10, 2) NOT NULL,
    PRIMARY KEY (payment_id, charge_id),
    CONSTRAINT allocations_amount_check CHECK (amount > 0)
);

CREATE INDEX idx_finance_allocations_charge ON finance.allocations (charge_id);

-- A payment reminder sent to a family (shown in their panel)
CREATE TABLE finance.reminders (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES people.students (id) ON DELETE CASCADE,
    message varchar(600) NOT NULL,
    overdue numeric(10, 2) NOT NULL,
    sent_name varchar(160) NOT NULL DEFAULT '',
    sent_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_finance_reminders_student ON finance.reminders (student_id, sent_at DESC);
