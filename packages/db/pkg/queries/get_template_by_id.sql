-- name: GetTemplateByID :one
SELECT t.*
FROM "public"."envs" t
WHERE t.id = $1;
