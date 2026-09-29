-- name: CreatePreference :exec
INSERT INTO preferences (user_id) VALUES (@user_id);

-- name: GetPreference :one
SELECT * FROM preferences WHERE user_id = @user_id;

-- name: UpdatePreference :one
UPDATE preferences
SET likes = @likes, neutral = @neutral, dislikes = @dislikes, max_budget_fen = @max_budget_fen,
    requires_wheelchair_access = @requires_wheelchair_access, version = version + 1
WHERE user_id = @user_id AND version = @version AND version < 9223372036854775807
RETURNING *;
