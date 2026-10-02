-- name: CreateOuting :one
INSERT INTO outings (group_id, initiator_id, timezone, duration_minutes, creation_key)
VALUES (@group_id, @actor, @timezone, @duration_minutes, @creation_key)
ON CONFLICT ON CONSTRAINT outings_creation_key DO NOTHING RETURNING *;

-- name: FindOutingCreation :one
SELECT * FROM outings WHERE initiator_id = @actor AND creation_key = @creation_key;

-- name: LockOutingGroup :one
SELECT id FROM groups WHERE id = @group_id FOR UPDATE;

-- name: CheckOutingPhase :exec
SELECT outing_check_phase(@outing_id::bigint, @phase::text);

-- name: ReadOuting :one
SELECT * FROM outings WHERE id = @outing_id;

-- name: ReadArrangement :one
SELECT * FROM outing_arrangements WHERE outing_id = @outing_id;

-- name: AddOutingDate :exec
INSERT INTO outing_dates (outing_id, date) VALUES (@outing_id, @date);

-- name: ReadOutingDates :many
SELECT date FROM outing_dates WHERE outing_id = @outing_id ORDER BY date;

-- name: InviteOutingGroup :exec
INSERT INTO outing_invitations (outing_id, member_id, phase)
SELECT o.id, m.member_id, 'decision' FROM outings o JOIN group_members m ON m.group_id = o.group_id
WHERE o.id = @outing_id ORDER BY m.member_id;

-- name: ReadInvitation :one
SELECT * FROM outing_invitations WHERE outing_id = @outing_id AND member_id = @member_id AND phase = @phase;

-- name: ListInvitations :many
SELECT i.* FROM outing_invitations i
WHERE ((@outing_id::bigint = 0 AND i.member_id = @actor::bigint) OR i.outing_id = @outing_id)
AND (i.outing_id, i.member_id, i.phase) > (@after_outing::bigint, @after_member::bigint, @after_phase::text)
ORDER BY i.outing_id, i.member_id, i.phase LIMIT @page_limit::int;

-- name: ReadMemberInput :one
SELECT * FROM outing_member_inputs WHERE outing_id = @outing_id AND member_id = @member_id;

-- name: ReadAvailability :many
SELECT * FROM outing_availability WHERE outing_id = @outing_id AND member_id = @member_id ORDER BY date, start_minute;

-- name: SetInvitationResponse :exec
UPDATE outing_invitations SET status = @status WHERE outing_id = @outing_id AND member_id = @member_id AND phase = @phase;

-- name: DeleteAvailability :exec
DELETE FROM outing_availability WHERE outing_id = @outing_id AND member_id = @member_id;

-- name: DeleteMemberInput :exec
DELETE FROM outing_member_inputs WHERE outing_id = @outing_id AND member_id = @member_id;

-- name: AddMemberInput :exec
INSERT INTO outing_member_inputs (outing_id, member_id, max_budget_fen, requires_wheelchair_access)
VALUES (@outing_id, @member_id, @max_budget_fen, @requires_wheelchair_access);

-- name: AddAvailability :exec
INSERT INTO outing_availability (outing_id, member_id, date, start_minute, end_minute)
VALUES (@outing_id, @member_id, @date, @start_minute, @end_minute);
