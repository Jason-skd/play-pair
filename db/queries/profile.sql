-- name: GetProfile :one
SELECT * FROM profiles WHERE user_id = @user_id;

-- name: UpdateProfile :one
UPDATE profiles SET display_name = @display_name, nickname = @nickname, version = version + 1
WHERE user_id = @user_id AND version = @version AND version < 9223372036854775807
RETURNING *;
