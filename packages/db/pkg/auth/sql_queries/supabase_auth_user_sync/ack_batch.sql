-- name: AckUserSyncQueueItems :exec
DELETE FROM public.user_sync_queue
WHERE id = ANY(sqlc.arg(ids)::bigint[]);
