DELETE FROM iam.sessions WHERE mfa_method = 'google';
ALTER TABLE iam.sessions DROP CONSTRAINT sessions_mfa_method_check;
ALTER TABLE iam.sessions ADD CONSTRAINT sessions_mfa_method_check
    CHECK (mfa_method IN ('email', 'totp', 'recovery_code'));

DROP TABLE IF EXISTS iam.user_identities;
