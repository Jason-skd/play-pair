-- name: GetProfile :one
SELECT user_id, display_name, nickname, version FROM profiles WHERE user_id = $1;

-- name: UpdateProfile :one
UPDATE profiles SET display_name = $2, nickname = $3, version = version + 1
WHERE user_id = $1 AND version = $4 AND version < 9223372036854775807
RETURNING user_id, display_name, nickname, version;
