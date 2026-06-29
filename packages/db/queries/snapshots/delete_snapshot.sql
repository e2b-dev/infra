-- name: DeleteSnapshot :exec
DELETE FROM "public"."snapshots"
WHERE sandbox_id = @sandbox_id
AND team_id = @team_id;
