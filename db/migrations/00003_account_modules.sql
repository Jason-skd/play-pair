-- +goose Up
-- Fail rather than lose a login identifier when upgrading an inconsistent account.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM users u LEFT JOIN password_credentials c ON c.user_id = u.id WHERE c.user_id IS NULL) THEN
        RAISE EXCEPTION 'account without password credential: repair before migrating';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE password_credentials ADD COLUMN email TEXT;
UPDATE password_credentials c SET email = u.email FROM users u WHERE u.id = c.user_id;
ALTER TABLE password_credentials ALTER COLUMN email SET NOT NULL;
ALTER TABLE password_credentials ADD CONSTRAINT password_credentials_email_key UNIQUE (email);

CREATE TABLE profiles (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 80),
    nickname TEXT CHECK (nickname IS NULL OR char_length(nickname) BETWEEN 1 AND 80),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);
INSERT INTO profiles (user_id, display_name) SELECT id, display_name FROM users;
ALTER TABLE users DROP COLUMN email, DROP COLUMN display_name;

-- +goose Down
ALTER TABLE users ADD COLUMN email TEXT, ADD COLUMN display_name TEXT;
UPDATE users u SET email = c.email FROM password_credentials c WHERE c.user_id = u.id;
UPDATE users u SET display_name = p.display_name FROM profiles p WHERE p.user_id = u.id;
ALTER TABLE users ALTER COLUMN email SET NOT NULL, ALTER COLUMN display_name SET NOT NULL;
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
ALTER TABLE users ADD CONSTRAINT users_display_name_check CHECK (char_length(display_name) BETWEEN 1 AND 80);
ALTER TABLE password_credentials DROP COLUMN email;
DROP TABLE profiles;
