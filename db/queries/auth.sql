-- name: CreateUser :exec
INSERT INTO users (id, email, display_name, created_at)
VALUES ($1, $2, $3, $4);

-- name: CreatePasswordCredential :exec
INSERT INTO password_credentials (user_id, password_hash, updated_at)
VALUES ($1, $2, $3);

-- name: FindPasswordCredentialByEmail :one
SELECT u.id, u.email, u.display_name, u.created_at, pc.password_hash, pc.updated_at
FROM users u
JOIN password_credentials pc ON pc.user_id = u.id
WHERE u.email = $1;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
VALUES ($1, $2, $3, $4);

-- name: FindSessionUser :one
SELECT u.id, u.email, u.display_name, u.created_at, s.expires_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > $2;

-- name: DeleteSession :execrows
DELETE FROM sessions WHERE token_hash = $1;
