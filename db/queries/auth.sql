-- name: CreateUser :exec
INSERT INTO users (id, email, password_hash, display_name, created_at)
VALUES ($1, $2, $3, $4, $5);

-- name: FindUserByEmail :one
SELECT id, email, password_hash, display_name, created_at
FROM users
WHERE email = $1;

-- name: FindUserByID :one
SELECT id, email, password_hash, display_name, created_at
FROM users
WHERE id = $1;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
VALUES ($1, $2, $3, $4);

-- name: FindSessionUser :one
SELECT u.id, u.email, u.password_hash, u.display_name, u.created_at, s.expires_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > $2;

-- name: DeleteSession :execrows
DELETE FROM sessions WHERE token_hash = $1;
