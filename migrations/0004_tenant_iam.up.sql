-- Instituciones (tenants), personas e identidad.
--
--   tenant.institution ─< tenant.service     servicios educativos con código modular
--   tenant.institution ─< tenant.campus      sedes / locales
--   tenant.institution ─< core.person        datos personales, propios de cada institución
--   core.person ─o student / staff / student_guardian
--
--   iam.account (global) ─< iam.membership >─ tenant.institution
--   Una cuenta (correo + contraseña) entra a varias instituciones: un docente
--   que trabaja en dos colegios tiene una cuenta y dos membresías, cada una con
--   su persona y sus roles en esa institución.
--
-- Toda tabla con institution_id expone UNIQUE (institution_id, id) para que
-- las demás la referencien con FK compuesta y no se crucen instituciones.

CREATE SCHEMA IF NOT EXISTS tenant;
CREATE SCHEMA IF NOT EXISTS core;

CREATE TABLE tenant.institution (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Subdominio / identificador en URL: joseph-lister.
    slug       text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name       text NOT NULL,
    legal_name text,
    ruc        char(11) UNIQUE CHECK (ruc ~ '^\d{11}$'),
    management text NOT NULL DEFAULT 'private' CHECK (management IN ('private', 'public')),
    dre        text,
    ugel       text,
    ubigeo     char(6) CHECK (ubigeo ~ '^\d{6}$'),
    address    text,
    phone      text,
    email      text,
    logo_key   text,
    timezone   text NOT NULL DEFAULT 'America/Lima',
    -- Estado de la suscripción SaaS.
    status     text NOT NULL DEFAULT 'trial' CHECK (status IN ('trial', 'active', 'suspended', 'cancelled')),
    plan       text NOT NULL DEFAULT 'standard',
    settings   jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Servicio educativo autorizado: modalidad + nivel, con su código modular y
-- resolución de creación. Un CEBA tiene EBA; un colegio, EBR Inicial,
-- Primaria y/o Secundaria.
CREATE TABLE tenant.service (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    modality_code  text NOT NULL REFERENCES curriculum.modality (code),
    level          text NOT NULL CHECK (level IN ('initial', 'primary', 'secondary')),
    modular_code   char(7) UNIQUE CHECK (modular_code ~ '^\d{7}$'),
    -- Forma de atención autorizada (EBA: presencial, semipresencial, a distancia).
    attention_form text NOT NULL DEFAULT 'in_person' CHECK (attention_form IN ('in_person', 'blended', 'distance')),
    resolution     text,
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, modality_code, level)
);

CREATE TABLE tenant.campus (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    name           text NOT NULL,
    -- Código de local (MINEDU).
    local_code     text,
    address        text,
    ubigeo         char(6) CHECK (ubigeo ~ '^\d{6}$'),
    is_main        boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, name)
);

CREATE UNIQUE INDEX campus_single_main ON tenant.campus (institution_id) WHERE is_main;

-- ---------------------------------------------------------------------------
-- Personas
-- ---------------------------------------------------------------------------

-- Cada institución es responsable de sus datos personales (Ley N.° 29733):
-- la misma persona en dos instituciones son dos filas independientes.
CREATE TABLE core.person (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id     uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    document_type      text NOT NULL DEFAULT 'dni'
                       CHECK (document_type IN ('dni', 'ce', 'passport', 'other')),
    document_number    text NOT NULL,
    given_names        text NOT NULL,
    paternal_surname   text NOT NULL,
    maternal_surname   text,
    birth_date         date,
    sex                text CHECK (sex IN ('female', 'male')),
    email              text,
    phone              text,
    address            text,
    ubigeo             char(6) CHECK (ubigeo ~ '^\d{6}$'),
    native_language    text NOT NULL DEFAULT 'es',
    reniec_verified_at timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, document_type, document_number),
    CONSTRAINT person_dni_format CHECK (document_type <> 'dni' OR document_number ~ '^\d{8}$')
);

CREATE INDEX person_name_idx ON core.person (institution_id, paternal_surname, maternal_surname, given_names);

CREATE TABLE core.student (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    person_id      uuid NOT NULL,
    -- Código del estudiante en SIAGIE, cuando ya fue asignado.
    siagie_code    text,
    occupation     text,
    status         text NOT NULL DEFAULT 'active'
                   CHECK (status IN ('active', 'inactive', 'graduated', 'withdrawn', 'transferred')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, person_id),
    UNIQUE (institution_id, siagie_code),
    FOREIGN KEY (institution_id, person_id) REFERENCES core.person (institution_id, id)
);

-- Personal: docentes, tutores, directivos, administrativos. Qué puede hacer
-- cada uno lo definen sus roles en iam, no esta tabla.
CREATE TABLE core.staff (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    person_id      uuid NOT NULL,
    honorific      text,
    position       text NOT NULL,
    specialty      text,
    hired_on       date,
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, person_id),
    FOREIGN KEY (institution_id, person_id) REFERENCES core.person (institution_id, id)
);

-- Apoderados / responsables. Obligatorio en la práctica para menores (EBR);
-- opcional en EBA, donde suele ser el cónyuge o el responsable de pagos.
CREATE TABLE core.student_guardian (
    institution_id           uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    student_id               uuid NOT NULL,
    person_id                uuid NOT NULL,
    relationship             text NOT NULL
                             CHECK (relationship IN ('mother', 'father', 'spouse', 'sibling', 'grandparent', 'legal_guardian', 'other')),
    is_financial_responsible boolean NOT NULL DEFAULT false,
    is_emergency_contact     boolean NOT NULL DEFAULT false,
    can_view_academic        boolean NOT NULL DEFAULT true,
    created_at               timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (institution_id, student_id, person_id),
    FOREIGN KEY (institution_id, student_id) REFERENCES core.student (institution_id, id) ON DELETE CASCADE,
    FOREIGN KEY (institution_id, person_id) REFERENCES core.person (institution_id, id)
);

CREATE INDEX student_guardian_person_idx ON core.student_guardian (institution_id, person_id);

-- ---------------------------------------------------------------------------
-- IAM
-- ---------------------------------------------------------------------------

CREATE TABLE iam.role (
    code        text PRIMARY KEY,
    name        text NOT NULL,
    description text
);

CREATE TABLE iam.permission (
    code        text PRIMARY KEY,
    module      text NOT NULL,
    description text NOT NULL
);

-- Matriz por defecto para toda institución nueva.
CREATE TABLE iam.role_permission (
    role_code       text NOT NULL REFERENCES iam.role (code) ON DELETE CASCADE,
    permission_code text NOT NULL REFERENCES iam.permission (code) ON DELETE CASCADE,
    PRIMARY KEY (role_code, permission_code)
);

-- Ajustes de una institución sobre la matriz por defecto (settings →
-- "Guardar políticas"). granted = false quita un permiso por defecto.
CREATE TABLE iam.institution_role_permission (
    institution_id  uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    role_code       text NOT NULL REFERENCES iam.role (code) ON DELETE CASCADE,
    permission_code text NOT NULL REFERENCES iam.permission (code) ON DELETE CASCADE,
    granted         boolean NOT NULL,
    PRIMARY KEY (institution_id, role_code, permission_code)
);

-- Credencial global. "user" es palabra reservada en PostgreSQL.
CREATE TABLE iam.account (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    email               text NOT NULL,
    display_name        text NOT NULL,
    -- Null hasta que el usuario define su contraseña (/auth/password/setup).
    password_hash       text,
    status              text NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'active', 'suspended', 'disabled')),
    mfa_enabled         boolean NOT NULL DEFAULT false,
    -- Secreto TOTP cifrado por la API; nunca se guarda en claro.
    mfa_secret          bytea,
    -- Operador de la plataforma SaaS (soporte, alta de instituciones).
    is_platform_admin   boolean NOT NULL DEFAULT false,
    locale              text NOT NULL DEFAULT 'es-PE',
    preferences         jsonb NOT NULL DEFAULT '{}',
    last_login_at       timestamptz,
    password_changed_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT account_mfa_secret CHECK (NOT mfa_enabled OR mfa_secret IS NOT NULL)
);

CREATE UNIQUE INDEX account_email_unique ON iam.account (lower(email));

-- Pertenencia de una cuenta a una institución, vinculada a su persona allí.
CREATE TABLE iam.membership (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES tenant.institution (id) ON DELETE CASCADE,
    account_id     uuid NOT NULL REFERENCES iam.account (id) ON DELETE CASCADE,
    person_id      uuid NOT NULL,
    status         text NOT NULL DEFAULT 'invited' CHECK (status IN ('invited', 'active', 'suspended')),
    invited_by     uuid REFERENCES iam.account (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (institution_id, id),
    UNIQUE (institution_id, account_id),
    UNIQUE (institution_id, person_id),
    FOREIGN KEY (institution_id, person_id) REFERENCES core.person (institution_id, id)
);

CREATE INDEX membership_account_idx ON iam.membership (account_id);

CREATE TABLE iam.membership_role (
    institution_id uuid NOT NULL,
    membership_id  uuid NOT NULL,
    role_code      text NOT NULL REFERENCES iam.role (code),
    granted_by     uuid REFERENCES iam.account (id),
    granted_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (membership_id, role_code),
    FOREIGN KEY (institution_id, membership_id) REFERENCES iam.membership (institution_id, id) ON DELETE CASCADE
);

-- Sesiones con refresh token. Se guarda solo el hash del token.
-- institution_id es la institución elegida en esa sesión.
CREATE TABLE iam.session (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id         uuid NOT NULL REFERENCES iam.account (id) ON DELETE CASCADE,
    institution_id     uuid REFERENCES tenant.institution (id) ON DELETE SET NULL,
    refresh_token_hash bytea NOT NULL UNIQUE,
    ip                 inet,
    user_agent         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    last_seen_at       timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    revoked_at         timestamptz
);

CREATE INDEX session_active_idx ON iam.session (account_id) WHERE revoked_at IS NULL;

-- Tokens de un solo uso: verificar correo, definir y recuperar contraseña.
CREATE TABLE iam.account_token (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id uuid NOT NULL REFERENCES iam.account (id) ON DELETE CASCADE,
    purpose    text NOT NULL CHECK (purpose IN ('email_verification', 'password_setup', 'password_reset')),
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE iam.mfa_recovery_code (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id uuid NOT NULL REFERENCES iam.account (id) ON DELETE CASCADE,
    code_hash  bytea NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX mfa_recovery_code_account_idx ON iam.mfa_recovery_code (account_id) WHERE used_at IS NULL;

-- Intentos de ingreso, exitosos o no. account_id es null si el correo no existe.
CREATE TABLE iam.login_event (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id     uuid REFERENCES iam.account (id) ON DELETE SET NULL,
    institution_id uuid REFERENCES tenant.institution (id) ON DELETE SET NULL,
    email          text NOT NULL,
    success        boolean NOT NULL,
    failure_reason text,
    ip             inet,
    user_agent     text,
    location       text,
    occurred_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX login_event_account_idx ON iam.login_event (account_id, occurred_at DESC);
CREATE INDEX login_event_ip_idx ON iam.login_event (ip, occurred_at DESC);

-- Permisos efectivos de cada membresía: matriz por defecto + ajustes de la
-- institución. Es lo que consulta la API para autorizar.
CREATE VIEW iam.membership_permission WITH (security_invoker = true) AS
SELECT DISTINCT mr.institution_id, mr.membership_id, rp.permission_code
FROM iam.membership_role mr
JOIN (
    SELECT i.id AS institution_id, rp.role_code, rp.permission_code
    FROM tenant.institution i
    CROSS JOIN iam.role_permission rp
    WHERE NOT EXISTS (
        SELECT 1 FROM iam.institution_role_permission o
        WHERE o.institution_id = i.id AND o.role_code = rp.role_code
          AND o.permission_code = rp.permission_code AND NOT o.granted)
    UNION
    SELECT o.institution_id, o.role_code, o.permission_code
    FROM iam.institution_role_permission o
    WHERE o.granted
) rp ON rp.institution_id = mr.institution_id AND rp.role_code = mr.role_code;

-- ---------------------------------------------------------------------------
-- Catálogo de roles y permisos (propuesta SIGE-JL §2 y matriz de settings)
-- ---------------------------------------------------------------------------

INSERT INTO iam.role (code, name, description) VALUES
    ('promoter',             'Promotor',              'Acceso ejecutivo completo e indicadores institucionales'),
    ('director',             'Director',              'Supervisión de ciclos, grados, docentes y documentos'),
    ('academic_coordinator', 'Coordinador académico', 'Seguimiento docente, sesiones y cumplimiento curricular'),
    ('tutor',                'Tutor',                 'Seguimiento individual, alertas e intervenciones'),
    ('teacher',              'Docente',               'Cursos, sesiones, materiales, evaluaciones y asistencia'),
    ('student',              'Estudiante',            'Aula virtual, evaluaciones, notas y progreso'),
    ('guardian',             'Padre / responsable',   'Asistencia, avances, comunicados y pagos'),
    ('secretary',            'Secretaría',            'Matrículas, expedientes, certificados y documentos'),
    ('admin',                'Administrador',         'Usuarios, permisos, seguridad y configuración'),
    ('accountant',           'Contador',              'Pensiones, pagos y reportes económicos');

INSERT INTO iam.permission (code, module, description) VALUES
    ('enrollment.manage', 'enrollment', 'Matrícula y homologación SIAGIE'),
    ('grades.write',      'assessment', 'Carga de calificaciones CNEB (AD-C)'),
    ('billing.issue',     'finance',    'Emisión de boletas SUNAT / OSE'),
    ('wellbeing.cases',   'wellbeing',  'Fichas psicológicas y casos de riesgo'),
    ('backup.manage',     'ops',        'Respaldos críticos y base de datos'),
    ('roster.export',     'enrollment', 'Exportación de padrón y DNI'),
    ('users.manage',      'iam',        'Gestión de cuentas, roles y permisos'),
    ('audit.view',        'audit',      'Consulta de bitácoras de auditoría'),
    ('reports.view',      'reports',    'Reportes e indicadores institucionales'),
    ('curriculum.manage', 'academic',   'Plan de estudios, secciones y aulas');

INSERT INTO iam.role_permission (role_code, permission_code)
SELECT 'promoter', code FROM iam.permission;

INSERT INTO iam.role_permission (role_code, permission_code) VALUES
    ('director',             'enrollment.manage'),
    ('director',             'grades.write'),
    ('director',             'wellbeing.cases'),
    ('director',             'roster.export'),
    ('director',             'audit.view'),
    ('director',             'reports.view'),
    ('director',             'curriculum.manage'),
    ('academic_coordinator', 'enrollment.manage'),
    ('academic_coordinator', 'grades.write'),
    ('academic_coordinator', 'wellbeing.cases'),
    ('academic_coordinator', 'roster.export'),
    ('academic_coordinator', 'reports.view'),
    ('academic_coordinator', 'curriculum.manage'),
    ('teacher',              'grades.write'),
    ('tutor',                'wellbeing.cases'),
    ('secretary',            'enrollment.manage'),
    ('secretary',            'roster.export'),
    ('accountant',           'billing.issue'),
    ('accountant',           'roster.export'),
    ('accountant',           'reports.view'),
    ('admin',                'users.manage'),
    ('admin',                'audit.view');

SELECT util.attach_updated_at('tenant');
SELECT util.attach_updated_at('core');
SELECT util.attach_updated_at('iam');

SELECT audit.attach('tenant.institution', 'UPDATE OR DELETE');
SELECT audit.attach('core.person', 'UPDATE OR DELETE');
SELECT audit.attach('iam.account', 'UPDATE OR DELETE');
SELECT audit.attach('iam.membership');
SELECT audit.attach('iam.membership_role');
SELECT audit.attach('iam.role_permission');
SELECT audit.attach('iam.institution_role_permission');
