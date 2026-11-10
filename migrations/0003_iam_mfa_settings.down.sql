ALTER TABLE iam.mfa_recovery_codes DROP CONSTRAINT mfa_recovery_codes_used_code_check;
ALTER TABLE iam.mfa_recovery_codes DROP COLUMN used_code;

DELETE FROM iam.sessions WHERE mfa_method = 'password';
ALTER TABLE iam.sessions DROP CONSTRAINT sessions_mfa_method_check;
ALTER TABLE iam.sessions ADD CONSTRAINT sessions_mfa_method_check
    CHECK (mfa_method IN ('email', 'totp', 'recovery_code', 'google'));

DELETE FROM iam.verification_challenges WHERE purpose IN ('mfa_enable', 'mfa_disable', 'mfa_recovery_codes');
ALTER TABLE iam.verification_challenges DROP CONSTRAINT verification_challenges_method_check;
ALTER TABLE iam.verification_challenges DROP CONSTRAINT verification_challenges_purpose_check;
ALTER TABLE iam.verification_challenges ADD CONSTRAINT verification_challenges_purpose_check
    CHECK (purpose IN ('sign_in', 'password_reset'));
ALTER TABLE iam.verification_challenges ADD CONSTRAINT verification_challenges_method_check CHECK (
    (purpose = 'sign_in' AND method IN ('email', 'totp')) OR (purpose = 'password_reset' AND method IS NULL)
);
