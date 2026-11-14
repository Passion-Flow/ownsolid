-- Downgrade must preserve every enrollment and its recovery authority.
LOCK TABLE admin_totp_factors IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM admin_totp_factors) THEN
        RAISE EXCEPTION 'ADMIN_TOTP_DOWNGRADE_HAS_FACTORS';
    END IF;
END
$$;

DROP TABLE admin_totp_recovery_codes;
DROP TABLE admin_totp_challenges;
DROP TABLE admin_totp_factors;
