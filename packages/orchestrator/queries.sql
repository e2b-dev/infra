-- name: GlobalVersion :one
SELECT version FROM status WHERE id = 1;

-- name: IncGlobalVersion :one
UPDATE status
SET
   version = version + 1,
   updated_at = current_timestamp()
WHERE
   id = 1
RETURNING version;

-- name: SetGlobalStatus :one
UPDATE status
SET
   version = version + 1,
   updated_at = current_timestamp(),
   status = sqlc.arg('status')
WHERE
   id = 1
RETURNING version;

-- name: CreateSandbox :exec
INSERT INTO sandboxes(id, status, started_at, deadline, global_version)
VALUES (
   sqlc.arg('id'),
   sqlc.arg('status'),
   sqlc.arg('started_at'),
   sqlc.arg('deadline'),
   (SELECT version FROM status WHERE status.id = 1)
);

-- name: ShutdownSandbox :exec
UPDATE sandboxes
SET
  version = version + 1,
  global_version = (SELECT version FROM status WHERE status.id = 1),
  updated_at = current_timestamp(),
  duration_ms = sqlc.arg('duration_ms'),
  status = sqlc.arg('status')
WHERE
  sandboxes.id = sqlc.arg('id');

-- name: UpdateSandboxDeadline :exec
UPDATE sandboxes
SET
  version = version + 1,
  global_version = (SELECT version FROM status WHERE status.id = 1),
  udpated_at = current_timestamp(),
  deadline = sqlc.arg('deadline'),
  status = 'running'
WHERE
  sandboxes.id = sqlc.arg('id');
