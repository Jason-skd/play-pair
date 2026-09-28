-- +goose Up
-- Stop the old application before migrating. Goose runs this remapping in one
-- transaction; the locks also prevent writes from racing the foreign-key copy.
LOCK TABLE users, password_credentials, profiles, preferences, sessions IN ACCESS EXCLUSIVE MODE;

ALTER TABLE users ADD COLUMN new_id BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY;
ALTER TABLE password_credentials ADD COLUMN new_user_id BIGINT;
ALTER TABLE profiles ADD COLUMN new_user_id BIGINT;
ALTER TABLE preferences ADD COLUMN new_user_id BIGINT;
ALTER TABLE sessions ADD COLUMN new_user_id BIGINT;

UPDATE password_credentials c SET new_user_id = u.new_id FROM users u WHERE c.user_id = u.id;
UPDATE profiles p SET new_user_id = u.new_id FROM users u WHERE p.user_id = u.id;
UPDATE preferences p SET new_user_id = u.new_id FROM users u WHERE p.user_id = u.id;
UPDATE sessions s SET new_user_id = u.new_id FROM users u WHERE s.user_id = u.id;

ALTER TABLE password_credentials ALTER COLUMN new_user_id SET NOT NULL;
ALTER TABLE profiles ALTER COLUMN new_user_id SET NOT NULL;
ALTER TABLE preferences ALTER COLUMN new_user_id SET NOT NULL;
ALTER TABLE sessions ALTER COLUMN new_user_id SET NOT NULL;

-- Dropping the old child columns removes their keys and the old session-user
-- index. Restore each constraint explicitly against the new primary key below.
ALTER TABLE password_credentials DROP COLUMN user_id;
ALTER TABLE profiles DROP COLUMN user_id;
ALTER TABLE preferences DROP COLUMN user_id;
ALTER TABLE sessions DROP COLUMN user_id;
ALTER TABLE users DROP COLUMN id;

ALTER TABLE users RENAME COLUMN new_id TO id;
ALTER SEQUENCE users_new_id_seq RENAME TO users_id_seq;
ALTER TABLE users ADD PRIMARY KEY (id);
ALTER TABLE users ADD CONSTRAINT users_id_positive CHECK (id > 0);

ALTER TABLE password_credentials RENAME COLUMN new_user_id TO user_id;
ALTER TABLE profiles RENAME COLUMN new_user_id TO user_id;
ALTER TABLE preferences RENAME COLUMN new_user_id TO user_id;
ALTER TABLE sessions RENAME COLUMN new_user_id TO user_id;

ALTER TABLE password_credentials ADD PRIMARY KEY (user_id);
ALTER TABLE profiles ADD PRIMARY KEY (user_id);
ALTER TABLE preferences ADD PRIMARY KEY (user_id);
ALTER TABLE password_credentials ADD FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE profiles ADD FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE preferences ADD FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE sessions ADD FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX sessions_user_id_idx ON sessions(user_id);

-- +goose Down
-- Original UUIDs cannot be reconstructed. Fail explicitly rather than fabricate
-- identities; returning to the UUID schema requires a pre-migration backup.
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION '00005 cannot restore original UUIDs; restore a pre-migration backup';
END $$;
-- +goose StatementEnd
