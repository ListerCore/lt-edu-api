-- Pensiones, pagos y comprobantes de cada institución.
--
--   fee_schedule  cronograma del periodo (matrícula + cuotas), opcionalmente por ciclo
--   agreement     beca, convenio o fraccionamiento del estudiante
--   charge        deuda concreta de una matrícula (una por cuota)
--   payment       dinero recibido (efectivo, Yape, transferencia…)
--   allocation    qué parte de cada pago cubre cada cargo (N:M)
--   receipt       comprobante electrónico SUNAT emitido por el pago
--
-- El estado de un cargo (pagado, parcial, por vencer, en mora) no se guarda:
-- se calcula en finance.charge_balance a partir de pagos y vencimiento.

CREATE SCHEMA IF NOT EXISTS finance;

CREATE TABLE finance.concept (
    code text PRIMARY KEY,
    name text NOT NULL
);

INSERT INTO finance.concept (code, name) VALUES
    ('enrollment',  'Matrícula'),
    ('tuition',     'Pensión'),
    ('admission',   'Cuota de ingreso'),
    ('certificate', 'Certificado de estudios'),
    ('other',       'Otros');

CREATE TABLE finance.fee_schedule (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL,
    period_id      uuid NOT NULL,
    concept_code   text NOT NULL REFERENCES finance.concept (code),
    -- Null = aplica a todos los ciclos del periodo.
    cycle_code     text REFERENCES curriculum.cycle (code),
    installment    smallint NOT NULL DEFAULT 1 CHECK (installment > 0),
    label          text NOT NULL,
    amount         numeric(10, 2) NOT NULL CHECK (amount > 0),
    due_on         date NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE NULLS NOT DISTINCT (period_id, concept_code, cycle_code, installment),
    FOREIGN KEY (institution_id, period_id) REFERENCES academic.period (institution_id, id)
);

CREATE TABLE finance.agreement (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id   uuid NOT NULL,
    student_id       uuid NOT NULL,
    period_id        uuid NOT NULL,
    kind             text NOT NULL CHECK (kind IN ('scholarship', 'discount', 'installment_plan', 'partner_agreement')),
    -- Institución del convenio, ej. "Asociación Artesanos Huamanga".
    partner_name     text,
    discount_percent numeric(5, 2) CHECK (discount_percent BETWEEN 0 AND 100),
    discount_amount  numeric(10, 2) CHECK (discount_amount >= 0),
    notes            text,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    approved_by      uuid REFERENCES iam.account (id),
    approved_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id),
    FOREIGN KEY (institution_id, period_id) REFERENCES academic.period (institution_id, id),
    CHECK (discount_percent IS NULL OR discount_amount IS NULL)
);

CREATE INDEX agreement_student_idx ON finance.agreement (student_id, period_id);

CREATE TABLE finance.charge (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id  uuid NOT NULL,
    enrollment_id   uuid NOT NULL,
    fee_schedule_id uuid,
    agreement_id    uuid,
    concept_code    text NOT NULL REFERENCES finance.concept (code),
    description     text NOT NULL,
    amount          numeric(10, 2) NOT NULL CHECK (amount > 0),
    discount        numeric(10, 2) NOT NULL DEFAULT 0 CHECK (discount >= 0),
    due_on          date NOT NULL,
    voided_at       timestamptz,
    void_reason     text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, enrollment_id) REFERENCES enrollment.enrollment (institution_id, id),
    FOREIGN KEY (institution_id, fee_schedule_id) REFERENCES finance.fee_schedule (institution_id, id),
    FOREIGN KEY (institution_id, agreement_id) REFERENCES finance.agreement (institution_id, id),
    CHECK (discount <= amount),
    CHECK ((voided_at IS NULL) = (void_reason IS NULL))
);

-- Una cuota del cronograma se cobra una sola vez por matrícula (salvo anulada).
CREATE UNIQUE INDEX charge_schedule_unique ON finance.charge (enrollment_id, fee_schedule_id)
    WHERE fee_schedule_id IS NOT NULL AND voided_at IS NULL;
CREATE INDEX charge_due_idx ON finance.charge (institution_id, due_on) WHERE voided_at IS NULL;

CREATE TABLE finance.payment (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id   uuid NOT NULL,
    student_id       uuid NOT NULL,
    payer_person_id  uuid,
    amount           numeric(10, 2) NOT NULL CHECK (amount > 0),
    method           text NOT NULL CHECK (method IN ('cash', 'bank_transfer', 'deposit', 'yape', 'plin', 'card')),
    bank             text,
    operation_number text,
    -- Captura del voucher / Yape enviado por el apoderado.
    evidence_key     text,
    paid_at          timestamptz NOT NULL,
    status           text NOT NULL DEFAULT 'pending_review'
                     CHECK (status IN ('pending_review', 'confirmed', 'rejected', 'reversed')),
    received_by      uuid REFERENCES iam.account (id),
    notes            text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id),
    FOREIGN KEY (institution_id, payer_person_id) REFERENCES core.person (institution_id, id)
);

-- Evita registrar dos veces el mismo voucher en la institución.
CREATE UNIQUE INDEX payment_operation_unique ON finance.payment (institution_id, method, bank, operation_number)
    NULLS NOT DISTINCT
    WHERE operation_number IS NOT NULL AND status <> 'rejected';
CREATE INDEX payment_student_idx ON finance.payment (student_id, paid_at DESC);

CREATE TABLE finance.allocation (
    institution_id uuid NOT NULL,
    payment_id     uuid NOT NULL,
    charge_id      uuid NOT NULL,
    amount         numeric(10, 2) NOT NULL CHECK (amount > 0),
    PRIMARY KEY (payment_id, charge_id),
    FOREIGN KEY (institution_id, payment_id) REFERENCES finance.payment (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, charge_id) REFERENCES finance.charge (institution_id, id)
);

CREATE INDEX allocation_charge_idx ON finance.allocation (charge_id);

-- Impide repartir más dinero del que entró o cubrir más de lo que se debe.
-- Diferido al final de la transacción para permitir insertar en cualquier orden.
CREATE FUNCTION finance.check_allocation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    allocated    numeric;
    limit_amount numeric;
BEGIN
    SELECT coalesce(sum(a.amount), 0), p.amount
      INTO allocated, limit_amount
      FROM finance.payment p
      LEFT JOIN finance.allocation a ON a.payment_id = p.id
     WHERE p.id = NEW.payment_id
     GROUP BY p.amount;

    IF allocated > limit_amount THEN
        RAISE EXCEPTION 'el pago % asigna % y solo tiene %', NEW.payment_id, allocated, limit_amount;
    END IF;

    SELECT coalesce(sum(a.amount), 0), c.amount - c.discount
      INTO allocated, limit_amount
      FROM finance.charge c
      LEFT JOIN finance.allocation a ON a.charge_id = c.id
     WHERE c.id = NEW.charge_id
     GROUP BY c.amount, c.discount;

    IF allocated > limit_amount THEN
        RAISE EXCEPTION 'el cargo % recibe % y solo debe %', NEW.charge_id, allocated, limit_amount;
    END IF;

    RETURN NULL;
END
$$;

CREATE CONSTRAINT TRIGGER allocation_limits
    AFTER INSERT OR UPDATE ON finance.allocation
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION finance.check_allocation();

CREATE TABLE finance.receipt (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id      uuid NOT NULL,
    payment_id          uuid NOT NULL,
    doc_type            text NOT NULL CHECK (doc_type IN ('boleta', 'factura', 'credit_note')),
    series              text NOT NULL CHECK (series ~ '^[A-Z0-9]{4}$'),
    number              integer NOT NULL CHECK (number > 0),
    customer_doc_type   text NOT NULL,
    customer_doc_number text NOT NULL,
    customer_name       text NOT NULL,
    subtotal            numeric(10, 2) NOT NULL,
    -- Servicios educativos exonerados de IGV: normalmente 0.
    tax                 numeric(10, 2) NOT NULL DEFAULT 0,
    total               numeric(10, 2) NOT NULL,
    issued_at           timestamptz NOT NULL DEFAULT now(),
    sunat_status        text NOT NULL DEFAULT 'pending'
                        CHECK (sunat_status IN ('pending', 'accepted', 'rejected', 'voided')),
    sunat_ticket        text,
    xml_storage_key     text,
    pdf_storage_key     text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    -- La serie es del RUC de cada institución.
    UNIQUE (institution_id, series, number),
    FOREIGN KEY (institution_id, payment_id) REFERENCES finance.payment (institution_id, id),
    CHECK (total = subtotal + tax)
);

CREATE INDEX receipt_payment_idx ON finance.receipt (payment_id);

-- Saldo y estado de cada cargo vigente. Solo cuentan pagos confirmados.
-- security_invoker: respeta el aislamiento por institución de las tablas base.
CREATE VIEW finance.charge_balance WITH (security_invoker = true) AS
SELECT
    c.institution_id,
    c.id AS charge_id,
    c.enrollment_id,
    c.concept_code,
    c.description,
    c.due_on,
    c.amount - c.discount AS net_amount,
    coalesce(paid.total, 0) AS paid_amount,
    c.amount - c.discount - coalesce(paid.total, 0) AS balance,
    CASE
        WHEN coalesce(paid.total, 0) >= c.amount - c.discount THEN 'paid'
        WHEN c.due_on < current_date THEN 'overdue'
        WHEN coalesce(paid.total, 0) > 0 THEN 'partial'
        WHEN c.due_on <= current_date + 7 THEN 'due_soon'
        ELSE 'scheduled'
    END AS status
FROM finance.charge c
LEFT JOIN LATERAL (
    SELECT sum(a.amount) AS total
    FROM finance.allocation a
    JOIN finance.payment p ON p.id = a.payment_id AND p.status = 'confirmed'
    WHERE a.charge_id = c.id
) paid ON true
WHERE c.voided_at IS NULL;

SELECT util.attach_updated_at('finance');

SELECT audit.attach('finance.agreement');
SELECT audit.attach('finance.charge');
SELECT audit.attach('finance.payment');
SELECT audit.attach('finance.receipt');
