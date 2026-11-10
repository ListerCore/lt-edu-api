-- External sign-in providers (Google) linked to an account. One account can link a provider once, and a provider
-- account can belong to one Joseph account only
CREATE TABLE iam.user_identities (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES iam.users (id) ON DELETE CASCADE,
    provider varchar(20) NOT NULL,
    -- Stable id at the provider (Google's "sub"); the email there may change, this never does
    subject varchar(255) NOT NULL,
    -- Email the provider reported at the last sign-in, for support only
    email varchar(254) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_identities_provider_check CHECK (provider IN ('google')),
    CONSTRAINT user_identities_provider_subject_key UNIQUE (provider, subject),
    CONSTRAINT user_identities_user_provider_key UNIQUE (user_id, provider),
    CONSTRAINT user_identities_subject_required CHECK (btrim(subject) <> '')
);

-- A Google sign-in is a session method of its own (Google already proved the mailbox)
ALTER TABLE iam.sessions DROP CONSTRAINT sessions_mfa_method_check;
ALTER TABLE iam.sessions ADD CONSTRAINT sessions_mfa_method_check
    CHECK (mfa_method IN ('email', 'totp', 'recovery_code', 'google'));
