-- name: RetryUserSyncQueueItem :exec
UPDATE auth.user_sync_queue
SET
    locked_at = NULL,
    lock_owner = NULL,
    next_attempt_at = now() + sqlc.arg(backoff)::interval,
    last_error = sqlc.arg(last_error)::text
WHERE id = sqlc.arg(id)::bigint;
