-- name: GetEnvAliasesByEnvIDs :many
SELECT
    env_id,
    ARRAY_AGG(alias ORDER BY alias)::text[] AS aliases,
    ARRAY_AGG(CASE WHEN namespace IS NOT NULL THEN namespace || '/' || alias ELSE alias END ORDER BY alias)::text[] AS names
FROM "public"."env_aliases"
WHERE env_id = ANY(@env_ids::text[])
GROUP BY env_id;
