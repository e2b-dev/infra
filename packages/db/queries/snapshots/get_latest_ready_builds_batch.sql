-- name: GetLatestReadyBuildsByEnvIDs :many
SELECT DISTINCT ON (eba.env_id) eba.env_id AS lookup_env_id, sqlc.embed(eb)
FROM "public"."env_build_assignments" eba
JOIN "public"."env_builds" eb ON eb.id = eba.build_id
WHERE
    eba.env_id = ANY(@env_ids::text[])
    AND eba.tag = 'default'
    AND eb.status_group = 'ready'
ORDER BY eba.env_id, eba.created_at DESC;
