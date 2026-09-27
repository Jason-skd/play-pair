-- name: CreateUser :exec
INSERT INTO users (id, created_at) VALUES ($1, $2);

-- name: CreatePasswordCredential :exec
INSERT INTO password_credentials (user_id, email, password_hash, updated_at) VALUES ($1, $2, $3, $4);

-- name: CreateProfile :exec
INSERT INTO profiles (user_id, display_name) VALUES ($1, $2);

-- name: FindPasswordCredentialByEmail :one
SELECT u.id, u.created_at, pc.email, pc.password_hash
FROM password_credentials pc JOIN users u ON u.id = pc.user_id WHERE pc.email = $1;

-- name: UserDetails :one
SELECT pc.email, p.display_name FROM users u
JOIN password_credentials pc ON pc.user_id = u.id
JOIN profiles p ON p.user_id = u.id WHERE u.id = $1;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ($1, $2, $3, $4);

-- name: FindSessionUser :one
SELECT u.id, u.created_at FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > $2;

-- name: DeleteSession :execrows
DELETE FROM sessions WHERE token_hash = $1;
