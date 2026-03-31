-- name: AckUserSyncQueueItem :exec
DELETE FROM public.user_sync_queue
WHERE id = sqlc.arg(id)::bigint;
