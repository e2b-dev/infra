-- name: DeleteTemplate :exec
DELETE FROM "public"."envs"
WHERE id = @template_id;