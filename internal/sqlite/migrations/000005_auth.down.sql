DROP TABLE recovery_codes;
DROP TABLE sessions;
ALTER TABLE users DROP COLUMN totp_enabled;
ALTER TABLE users DROP COLUMN totp_secret;
ALTER TABLE users DROP COLUMN is_admin;
ALTER TABLE users DROP COLUMN password_hash;
