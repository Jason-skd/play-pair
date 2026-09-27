-- +goose Up
CREATE TABLE preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    likes TEXT NOT NULL DEFAULT '' CHECK (char_length(likes) <= 500),
    neutral TEXT NOT NULL DEFAULT '' CHECK (char_length(neutral) <= 500),
    dislikes TEXT NOT NULL DEFAULT '' CHECK (char_length(dislikes) <= 500),
    max_budget_fen BIGINT CHECK (max_budget_fen IS NULL OR max_budget_fen >= 0),
    requires_wheelchair_access BOOLEAN,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);
INSERT INTO preferences (user_id) SELECT id FROM users;

-- +goose Down
DROP TABLE preferences;
