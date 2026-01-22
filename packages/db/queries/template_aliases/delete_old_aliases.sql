-- name: DeleteOtherTemplateAliases :many
DELETE FROM "public"."env_aliases"
WHERE env_id = $1
  AND is_renamable = TRUE
RETURNING alias;