-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 80),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE password_credentials (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at)
);

CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

-- Database-owned timestamps keep account rows consistent across all writers.
-- +goose StatementBegin
CREATE FUNCTION users_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        NEW.created_at := clock_timestamp();
        NEW.updated_at := NEW.created_at;
    ELSIF NEW IS DISTINCT FROM OLD THEN
        NEW.updated_at := greatest(OLD.updated_at, clock_timestamp());
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION credentials_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW IS DISTINCT FROM OLD THEN
        NEW.updated_at := CASE WHEN TG_OP = 'INSERT' THEN clock_timestamp() ELSE greatest(OLD.updated_at, clock_timestamp()) END;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION sessions_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.created_at := clock_timestamp();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER users_stamp BEFORE INSERT OR UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION users_stamp();
CREATE TRIGGER credentials_stamp BEFORE INSERT OR UPDATE ON password_credentials
    FOR EACH ROW EXECUTE FUNCTION credentials_stamp();
CREATE TRIGGER sessions_stamp BEFORE INSERT ON sessions
    FOR EACH ROW EXECUTE FUNCTION sessions_stamp();

-- +goose Down
DROP TABLE sessions;
DROP TABLE password_credentials;
DROP FUNCTION credentials_stamp();
DROP FUNCTION sessions_stamp();
DROP FUNCTION users_stamp();
DROP TABLE users;
