-- name: Example :one
SELECT *
FROM "public"."teams" t WHERE t.id = $1;
