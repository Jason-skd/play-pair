-- name: CreateUser :one
INSERT INTO users DEFAULT VALUES RETURNING id, created_at;

-- name: CreatePasswordCredential :exec
INSERT INTO password_credentials (user_id, email, password_hash) VALUES (@user_id, @email, @password_hash);

-- name: CreateProfile :exec
INSERT INTO profiles (user_id, display_name) VALUES (@user_id, @display_name);

-- name: FindPasswordCredentialByEmail :one
SELECT u.id, u.created_at, pc.email, pc.password_hash
FROM password_credentials pc JOIN users u ON u.id = pc.user_id WHERE pc.email = @email;

-- name: UserDetails :one
SELECT pc.email, p.display_name FROM users u
JOIN password_credentials pc ON pc.user_id = u.id
JOIN profiles p ON p.user_id = u.id WHERE u.id = @user_id;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (@token_hash, @user_id, @expires_at);

-- name: FindSessionUser :one
SELECT u.id, u.created_at FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = @token_hash AND s.expires_at > @now;

-- name: DeleteSession :execrows
DELETE FROM sessions WHERE token_hash = @token_hash;
