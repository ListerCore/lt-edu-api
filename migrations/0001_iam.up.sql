-- Identity and access: tenants, accounts, roles per institution, sessions and the second factor
CREATE SCHEMA IF NOT EXISTS iam;

-- Keeps updated_at honest on every UPDATE
CREATE FUNCTION iam.touch_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

-- Tenants: every institution-scoped row hangs from one of these
CREATE TABLE iam.institutions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    slug varchar(63) NOT NULL,
    name varchar(200) NOT NULL,
    -- Institution type: drives academic structure, periods, grading and active modules
    kind varchar(20) NOT NULL,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT institutions_slug_key UNIQUE (slug),
    CONSTRAINT institutions_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT institutions_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT institutions_kind_check CHECK (kind IN ('ebr', 'eba', 'ebe', 'cepre', 'institute'))
);

CREATE TRIGGER institutions_touch_updated_at BEFORE UPDATE ON iam.institutions
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Accounts. One per person and email, whatever institutions or roles they hold
CREATE TABLE iam.users (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    email varchar(254) NOT NULL,
    full_name varchar(200) NOT NULL,
    -- Argon2id PHC string; NULL while the account has no password (pending invitation, Google only)
    password_hash text,
    avatar_url text,
    status varchar(20) NOT NULL DEFAULT 'active',
    -- Set the first time a code sent to the email is confirmed
    email_verified_at timestamptz,
    password_changed_at timestamptz,
    -- Brute-force lockout per account (the per-IP brake lives in the API)
    failed_sign_in_attempts integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    last_sign_in_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_key UNIQUE (email),
    CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email)) AND email LIKE '_%@_%'),
    CONSTRAINT users_full_name_required CHECK (btrim(full_name) <> ''),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'suspended')),
    CONSTRAINT users_failed_attempts_check CHECK (failed_sign_in_attempts >= 0)
);

CREATE TRIGGER users_touch_updated_at BEFORE UPDATE ON iam.users
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- Role catalog. scope says where a role applies: the whole platform (Joseph's team) or one institution
CREATE TABLE iam.roles (
    id smallint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code varchar(40) NOT NULL,
    name varchar(100) NOT NULL,
    description varchar(255) NOT NULL,
    scope varchar(20) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT roles_code_key UNIQUE (code),
    -- Target of the composite foreign keys that check the scope of every assignment
    CONSTRAINT roles_id_scope_key UNIQUE (id, scope),
    CONSTRAINT roles_code_format CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT roles_name_required CHECK (btrim(name) <> ''),
    CONSTRAINT roles_description_required CHECK (btrim(description) <> ''),
    CONSTRAINT roles_scope_check CHECK (scope IN ('platform', 'institution'))
);

INSERT INTO iam.roles (code, name, description, scope) VALUES
    ('superadmin',           'Superadmin',            'Gestiona instituciones, planes y módulos activos. Solo equipo de Joseph.',          'platform'),
    ('director',             'Director',              'Toda su institución: configuración, reportes, finanzas y cierres de periodo.',       'institution'),
    ('academic_coordinator', 'Coordinador académico', 'Estructura académica, periodos, cursos, horarios, notas y actas.',                   'institution'),
    ('secretary',            'Secretaría',            'Matrícula, estudiantes, documentos y carnets.',                                      'institution'),
    ('teacher',              'Docente',               'Sus cursos y secciones: asistencia, notas y comunicados a sus estudiantes.',         'institution'),
    ('tutor',                'Tutor',                 'Su sección: acompañamiento, entrevistas y alertas.',                                 'institution'),
    ('assistant',            'Auxiliar',              'Asistencia diaria, tardanzas e incidencias.',                                        'institution'),
    ('psychologist',         'Psicólogo',             'Psicología y bienestar; único rol con acceso a notas confidenciales.',              'institution'),
    ('treasurer',            'Tesorería',             'Pensiones, pagos y reportes financieros.',                                           'institution'),
    ('student',              'Estudiante',            'Su horario, notas, asistencia, carnet y comunicados.',                               'institution'),
    ('guardian',             'Apoderado',             'Lo mismo que el estudiante para cada hijo a cargo, más pagos.',                     'institution');

-- Who holds which role where. Platform roles carry no institution; institution roles always carry one
CREATE TABLE iam.user_roles (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    role_id smallint NOT NULL,
    role_scope varchar(20) NOT NULL,
    institution_id uuid REFERENCES iam.institutions (id) ON DELETE CASCADE,
    granted_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    granted_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_roles_role_fkey FOREIGN KEY (role_id, role_scope) REFERENCES iam.roles (id, scope),
    CONSTRAINT user_roles_scope_check CHECK ((role_scope = 'platform') = (institution_id IS NULL)),
    CONSTRAINT user_roles_assignment_key UNIQUE NULLS NOT DISTINCT (user_id, role_id, institution_id),
    -- Lets sessions point at an assignment of their own user only
    CONSTRAINT user_roles_id_user_key UNIQUE (id, user_id)
);

CREATE INDEX idx_iam_user_roles_institution ON iam.user_roles (institution_id, role_id) WHERE institution_id IS NOT NULL;

-- Roster each institution loads: signing up with one of these emails grants the role (the person never picks it)
CREATE TABLE iam.invitations (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    institution_id uuid NOT NULL REFERENCES iam.institutions (id) ON DELETE CASCADE,
    email varchar(254) NOT NULL,
    role_id smallint NOT NULL,
    role_scope varchar(20) NOT NULL DEFAULT 'institution',
    invited_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    accepted_at timestamptz,
    accepted_by uuid REFERENCES iam.users (id) ON DELETE SET NULL,
    revoked_at timestamptz,
    CONSTRAINT invitations_role_fkey FOREIGN KEY (role_id, role_scope) REFERENCES iam.roles (id, scope),
    CONSTRAINT invitations_institution_role_check CHECK (role_scope = 'institution'),
    CONSTRAINT invitations_email_normalized CHECK (email = lower(btrim(email)) AND email LIKE '_%@_%'),
    CONSTRAINT invitations_state_check CHECK (accepted_at IS NULL OR revoked_at IS NULL),
    -- accepted_by goes NULL if that account is later deleted; the acceptance itself stays on record
    CONSTRAINT invitations_accepted_by_check CHECK (accepted_by IS NULL OR accepted_at IS NOT NULL)
);

CREATE UNIQUE INDEX uq_iam_invitations_pending ON iam.invitations (institution_id, email, role_id)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX idx_iam_invitations_pending_email ON iam.invitations (email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- Signed-in browsers. Only the SHA-256 of the token is stored; the token itself lives in the httpOnly cookie
CREATE TABLE iam.sessions (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    -- Role the person is working as; switching roles never needs a new sign-in
    active_user_role_id uuid,
    -- How the second step was passed
    mfa_method varchar(20) NOT NULL,
    ip inet,
    user_agent varchar(512),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT sessions_token_hash_key UNIQUE (token_hash),
    CONSTRAINT sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_active_role_fkey FOREIGN KEY (active_user_role_id, user_id)
        REFERENCES iam.user_roles (id, user_id) ON DELETE SET NULL (active_user_role_id),
    CONSTRAINT sessions_mfa_method_check CHECK (mfa_method IN ('email', 'totp', 'recovery_code')),
    CONSTRAINT sessions_expiration_check CHECK (expires_at > created_at)
);

CREATE INDEX idx_iam_sessions_user_live ON iam.sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_iam_sessions_expiration ON iam.sessions (expires_at);

-- Short-lived proofs: the second step of a sign-in and password reset links. Only hashes are stored
CREATE TABLE iam.verification_challenges (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    purpose varchar(30) NOT NULL,
    -- Handle held by the browser (sign-in cookie) or sent in the link (password reset)
    token_hash bytea NOT NULL,
    -- Second step of a sign-in: a code sent by email, or the authenticator app
    method varchar(20),
    -- 6-digit email code
    code_hash bytea,
    code_sent_at timestamptz,
    attempts smallint NOT NULL DEFAULT 0,
    ip inet,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT verification_challenges_token_hash_key UNIQUE (token_hash),
    CONSTRAINT verification_challenges_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT verification_challenges_purpose_check CHECK (purpose IN ('sign_in', 'password_reset')),
    CONSTRAINT verification_challenges_method_check CHECK (
        (purpose = 'sign_in' AND method IN ('email', 'totp')) OR (purpose = 'password_reset' AND method IS NULL)
    ),
    CONSTRAINT verification_challenges_email_code_check CHECK ((method = 'email') = (code_hash IS NOT NULL AND code_sent_at IS NOT NULL)),
    CONSTRAINT verification_challenges_attempts_check CHECK (attempts >= 0),
    CONSTRAINT verification_challenges_expiration_check CHECK (expires_at > created_at)
);

CREATE INDEX idx_iam_challenges_user_open ON iam.verification_challenges (user_id, purpose) WHERE consumed_at IS NULL;
CREATE INDEX idx_iam_challenges_expiration ON iam.verification_challenges (expires_at);

-- Enrolled second factors beyond email (every account can always receive a code by email).
-- TOTP secrets are encrypted with APP__SECRETS__KEY (AES-256-GCM); the key never touches the database
CREATE TABLE iam.mfa_factors (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    kind varchar(20) NOT NULL,
    secret_ciphertext bytea NOT NULL,
    key_version smallint NOT NULL DEFAULT 1,
    -- NULL until the first code from the app is confirmed; only confirmed factors are asked for at sign-in
    confirmed_at timestamptz,
    -- Last accepted 30-second step, so a code can never be replayed
    last_used_step bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mfa_factors_user_kind_key UNIQUE (user_id, kind),
    CONSTRAINT mfa_factors_kind_check CHECK (kind IN ('totp')),
    CONSTRAINT mfa_factors_key_version_check CHECK (key_version > 0)
);

CREATE TRIGGER mfa_factors_touch_updated_at BEFORE UPDATE ON iam.mfa_factors
    FOR EACH ROW EXECUTE FUNCTION iam.touch_updated_at();

-- One-use codes to get in when the authenticator app is lost; issued together when TOTP is confirmed
CREATE TABLE iam.mfa_recovery_codes (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    code_hash bytea NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT mfa_recovery_codes_user_hash_key UNIQUE (user_id, code_hash),
    CONSTRAINT mfa_recovery_codes_hash_length CHECK (octet_length(code_hash) = 32)
);

CREATE INDEX idx_iam_recovery_codes_available ON iam.mfa_recovery_codes (user_id) WHERE used_at IS NULL;
