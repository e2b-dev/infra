-- name: PurgeUserAccessTokens :exec
DELETE FROM public.access_tokens
WHERE user_id = sqlc.arg(user_id)::uuid;
