-- name: ClaimUserSyncQueueBatch :many
UPDATE public.user_sync_queue
SET
    locked_at = now(),
    lock_owner = sqlc.arg(lock_owner)::text,
    attempt_count = attempt_count + 1
WHERE id IN (
    SELECT id
    FROM public.user_sync_queue
    WHERE dead_lettered_at IS NULL
      AND next_attempt_at <= now()
      AND (locked_at IS NULL OR locked_at < now() - sqlc.arg(lock_timeout)::interval)
    ORDER BY id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)::int
)
RETURNING id, user_id, operation, created_at, attempt_count;
