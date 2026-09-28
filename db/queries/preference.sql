-- name: CreatePreference :exec
INSERT INTO preferences (user_id) VALUES ($1);

-- name: GetPreference :one
SELECT * FROM preferences WHERE user_id = $1;

-- name: UpdatePreference :one
UPDATE preferences
SET likes = $2, neutral = $3, dislikes = $4, max_budget_fen = $5,
    requires_wheelchair_access = $6, version = version + 1
WHERE user_id = $1 AND version = $7 AND version < 9223372036854775807
RETURNING *;
