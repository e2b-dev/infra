-- name: DeleteTemplate :exec
DELETE FROM "public"."envs"
WHERE id = @template_id
AND team_id = @team_id;