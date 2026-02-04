-- name: GetTeamAPIKeysWithCreator :many
SELECT
    tak.id,
    tak.name,
    tak.api_key_prefix,
    tak.api_key_length,
    tak.api_key_mask_prefix,
    tak.api_key_mask_suffix,
    tak.created_by as created_by_id,
    tak.created_at,
    tak.last_used,
    u.email AS created_by_email
FROM "public"."team_api_keys" tak
LEFT JOIN "auth"."users" u ON tak.created_by = u.id
WHERE tak.team_id = @team_id;