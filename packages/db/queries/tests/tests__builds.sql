-- name: Test_CreateEnv :exec
INSERT INTO "public"."envs" (id, team_id, created_at, updated_at)
VALUES ($1, $2, NOW(), NOW());