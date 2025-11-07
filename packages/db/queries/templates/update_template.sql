-- name: UpdateTemplate :one
UPDATE "public"."envs" e
SET public = @public
FROM "public"."env_aliases" ea
WHERE
    ea.env_id = e.id
  AND (e.id = @template_id_or_alias OR ea.alias = @template_id_or_alias)
  AND e.team_id = @team_id
RETURNING e.id;
