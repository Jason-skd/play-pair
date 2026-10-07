-- name: OutingGroupMember :one
SELECT EXISTS (SELECT 1 FROM group_members WHERE group_id= @group_id AND member_id= @member_id)::boolean;
-- name: InsertOuting :one
INSERT INTO outings(group_id,initiator_id,timezone,duration_minutes) VALUES(@group_id,@initiator_id,@timezone,@duration_minutes) RETURNING *;
-- name: ReadOuting :one
SELECT * FROM outings WHERE id= @outing_id;
-- name: LockOuting :one
SELECT id FROM outings WHERE id= @outing_id FOR UPDATE;
-- name: ReadOutingDates :many
SELECT date FROM outing_dates WHERE outing_id= @outing_id ORDER BY date;
-- name: AddOutingDate :exec
INSERT INTO outing_dates(outing_id,date) VALUES(@outing_id,@date);
-- name: DeleteOutingDates :exec
DELETE FROM outing_dates WHERE outing_id= @outing_id;
-- name: UpdateOutingPlan :exec
UPDATE outings SET duration_minutes= @duration_minutes WHERE id= @outing_id;
-- name: ReadArrangement :one
SELECT * FROM outing_arrangements WHERE outing_id= @outing_id;
-- name: InsertArrangement :exec
INSERT INTO outing_arrangements(outing_id,starts_at,ends_at,place,activity) VALUES(@outing_id,@starts_at,@ends_at,@place,@activity);
-- name: UpdateArrangement :exec
UPDATE outing_arrangements SET starts_at= @starts_at,ends_at= @ends_at,place= @place,activity= @activity WHERE outing_id= @outing_id;
-- name: ResetConfirmations :exec
UPDATE outing_confirmations SET choice=NULL WHERE outing_id= @outing_id;
-- name: OutingMembership :one
SELECT
 EXISTS(SELECT 1 FROM group_members m WHERE m.group_id= @group_id AND m.member_id= @member_id)::boolean AS in_group,
 EXISTS(SELECT 1 FROM outing_decisions d WHERE d.outing_id= @outing_id AND d.member_id= @member_id)::boolean AS has_decision,
 EXISTS(SELECT 1 FROM outing_confirmations c WHERE c.outing_id= @outing_id AND c.member_id= @member_id)::boolean AS has_confirmation;
-- name: ReadDecision :one
SELECT * FROM outing_decisions d WHERE d.outing_id= @outing_id AND member_id= @member_id;
-- name: ReadConfirmation :one
SELECT * FROM outing_confirmations c WHERE c.outing_id= @outing_id AND member_id= @member_id;
-- name: ReadAvailability :many
SELECT * FROM outing_availability WHERE outing_id= @outing_id AND member_id= @member_id ORDER BY date,start_minute;
-- name: SaveDecision :exec
INSERT INTO outing_decisions(outing_id,member_id,choice,max_budget_fen,requires_wheelchair_access)
VALUES(@outing_id,@member_id,@choice,sqlc.narg(max_budget_fen),sqlc.narg(requires_wheelchair_access))
ON CONFLICT(outing_id,member_id) DO UPDATE SET choice=excluded.choice,max_budget_fen=excluded.max_budget_fen,requires_wheelchair_access=excluded.requires_wheelchair_access;
-- name: DeleteAvailability :exec
DELETE FROM outing_availability WHERE outing_id= @outing_id AND member_id= @member_id;
-- name: AddAvailability :exec
INSERT INTO outing_availability(outing_id,member_id,date,start_minute,end_minute) VALUES(@outing_id,@member_id,@date,@start_minute,@end_minute);
-- name: SaveConfirmation :exec
INSERT INTO outing_confirmations(outing_id,member_id,choice) VALUES(@outing_id,@member_id,@choice)
ON CONFLICT(outing_id,member_id) DO UPDATE SET choice=excluded.choice;
-- name: ListVisibleOutings :many
SELECT o.* FROM outings o
WHERE o.id> @after_id AND (@group_filter::bigint=0 OR o.group_id= @group_filter)
AND (o.initiator_id= @actor OR EXISTS(SELECT 1 FROM group_members m WHERE m.group_id=o.group_id AND m.member_id= @actor)
 OR EXISTS(SELECT 1 FROM outing_decisions d WHERE d.outing_id=o.id AND d.member_id= @actor)
 OR EXISTS(SELECT 1 FROM outing_confirmations c WHERE c.outing_id=o.id AND c.member_id= @actor))
ORDER BY o.id LIMIT @page_limit;
-- name: ListOutingResponses :many
WITH decision_members AS (
 SELECT d.member_id FROM outing_decisions d WHERE d.outing_id= @outing_id
 UNION SELECT m.member_id FROM group_members m WHERE m.group_id= @group_id AND @decision_open::boolean
 UNION SELECT @initiator_id::bigint WHERE @decision_open::boolean
), confirmation_members AS (
 SELECT m.member_id FROM group_members m WHERE m.group_id= @group_id
 UNION SELECT d.member_id FROM outing_decisions d WHERE d.outing_id= @outing_id
 UNION SELECT c.member_id FROM outing_confirmations c WHERE c.outing_id= @outing_id
 UNION SELECT @initiator_id::bigint
), summaries AS (
 SELECT m.member_id,'decision'::text AS phase,coalesce(d.choice,'pending')::text AS status
 FROM decision_members m LEFT JOIN outing_decisions d ON d.outing_id= @outing_id AND d.member_id=m.member_id
 UNION ALL
 SELECT m.member_id,'confirmation'::text AS phase,coalesce(c.choice,'pending')::text AS status
 FROM confirmation_members m LEFT JOIN outing_confirmations c ON c.outing_id= @outing_id AND c.member_id=m.member_id
 WHERE NOT @decision_open::boolean
)
SELECT member_id,phase,status FROM summaries
WHERE (@phase_filter::text='' OR phase= @phase_filter) AND (@status_filter::text='' OR status= @status_filter)
AND (member_id,phase)>(@after_member::bigint,@after_phase::text)
ORDER BY member_id,phase LIMIT @page_limit;
-- name: ReadAcceptedDecisions :many
SELECT * FROM outing_decisions WHERE outing_id= @outing_id AND choice='accept'
ORDER BY member_id LIMIT 129;
-- name: ReadDecisionAvailability :many
SELECT * FROM outing_availability WHERE outing_id= @outing_id AND member_id= @member_id
ORDER BY date,start_minute LIMIT 257;
