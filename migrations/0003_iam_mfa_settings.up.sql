-- Two-step verification is opt-in: without an authenticator app the password alone signs in. Turning the app on or
-- off, and issuing new recovery codes, is confirmed with a code mailed to the account

ALTER TABLE iam.verification_challenges DROP CONSTRAINT verification_challenges_purpose_check;
ALTER TABLE iam.verification_challenges DROP CONSTRAINT verification_challenges_method_check;
ALTER TABLE iam.verification_challenges ADD CONSTRAINT verification_challenges_purpose_check
    CHECK (purpose IN ('sign_in', 'password_reset', 'mfa_enable', 'mfa_disable', 'mfa_recovery_codes'));
ALTER TABLE iam.verification_challenges ADD CONSTRAINT verification_challenges_method_check CHECK (
    (purpose = 'sign_in' AND method IN ('email', 'totp'))
    OR (purpose = 'password_reset' AND method IS NULL)
    OR (purpose IN ('mfa_enable', 'mfa_disable', 'mfa_recovery_codes') AND method = 'email')
);

-- "password": signed in with the password alone (no authenticator app enabled)
ALTER TABLE iam.sessions DROP CONSTRAINT sessions_mfa_method_check;
ALTER TABLE iam.sessions ADD CONSTRAINT sessions_mfa_method_check
    CHECK (mfa_method IN ('password', 'email', 'totp', 'recovery_code', 'google'));

-- A spent recovery code is shown back to its owner (it no longer opens anything); unused ones stay hashed only
ALTER TABLE iam.mfa_recovery_codes ADD COLUMN used_code varchar(11);
ALTER TABLE iam.mfa_recovery_codes ADD CONSTRAINT mfa_recovery_codes_used_code_check
    CHECK (used_code IS NULL OR used_at IS NOT NULL);
