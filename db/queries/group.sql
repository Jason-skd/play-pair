-- name: CreateGroup :one
INSERT INTO groups (name, join_code, initiator_id, creation_key)
VALUES ($1, $2, $3, $4)
ON CONFLICT ON CONSTRAINT groups_creation_key DO NOTHING
RETURNING *;

-- name: FindGroupCreation :one
SELECT * FROM groups WHERE initiator_id = $1 AND creation_key = $2;

-- name: LockGroupByCode :one
SELECT * FROM groups WHERE join_code = $1 FOR UPDATE;

-- name: AddGroupMember :exec
INSERT INTO group_members (group_id, member_id) VALUES ($1, $2)
ON CONFLICT ON CONSTRAINT group_members_pkey DO NOTHING;

-- name: ReadGroup :one
SELECT * FROM groups WHERE id = $1;

-- name: GroupAccess :one
SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = g.id AND member_id = sqlc.arg(actor)::bigint) AS allowed
FROM groups g WHERE g.id = $1;

-- name: ListGroupMembers :many
SELECT m.member_id, m.joined_at, p.display_name, p.nickname, g.initiator_id
FROM group_members m
JOIN groups g ON g.id = m.group_id
JOIN profiles p ON p.user_id = m.member_id
WHERE m.group_id = $1 AND m.member_id > sqlc.arg(after_member_id)::bigint
AND EXISTS (SELECT 1 FROM group_members a WHERE a.group_id = m.group_id AND a.member_id = sqlc.arg(actor)::bigint)
ORDER BY m.member_id LIMIT sqlc.arg(page_limit)::int;

-- name: ReadAuthorizedGroup :one
SELECT g.* FROM groups g
WHERE g.id = $1 AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = g.id AND m.member_id = sqlc.arg(actor)::bigint);
