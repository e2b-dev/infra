-- name: AckUserSyncQueueItem :exec
DELETE FROM auth.user_sync_queue
WHERE id = sqlc.arg(id)::bigint;
