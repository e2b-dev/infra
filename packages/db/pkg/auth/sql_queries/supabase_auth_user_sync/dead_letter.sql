-- name: DeadLetterUserSyncQueueItem :exec
UPDATE public.user_sync_queue
SET
    locked_at = NULL,
    lock_owner = NULL,
    dead_lettered_at = now(),
    last_error = sqlc.arg(last_error)::text
WHERE id = sqlc.arg(id)::bigint;
