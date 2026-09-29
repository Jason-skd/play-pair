-- +goose Up
CREATE TABLE preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    likes TEXT NOT NULL DEFAULT '' CHECK (char_length(likes) <= 500),
    neutral TEXT NOT NULL DEFAULT '' CHECK (char_length(neutral) <= 500),
    dislikes TEXT NOT NULL DEFAULT '' CHECK (char_length(dislikes) <= 500),
    max_budget_fen BIGINT CHECK (max_budget_fen IS NULL OR max_budget_fen >= 0),
    requires_wheelchair_access BOOLEAN,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);
INSERT INTO preferences (user_id) SELECT id FROM users;

-- +goose StatementBegin
CREATE FUNCTION preferences_stamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW IS DISTINCT FROM OLD THEN
        NEW.updated_at := CASE WHEN TG_OP = 'INSERT' THEN clock_timestamp() ELSE greatest(OLD.updated_at, clock_timestamp()) END;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER preferences_stamp BEFORE INSERT OR UPDATE ON preferences
    FOR EACH ROW EXECUTE FUNCTION preferences_stamp();

-- +goose Down
DROP TABLE preferences;
DROP FUNCTION preferences_stamp();
