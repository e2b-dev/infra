-- name: LockEnvForUpdate :one
SELECT id FROM "public"."envs"
WHERE id = @env_id
FOR UPDATE;
