-- Cluster-local teardown of one user's state, for the management interface's
-- purge route. public.users is deliberately left standing.

-- Returns the projects it touched, so the caller evicts exactly what it
-- removed. Reading them beforehand instead would be a second statement
-- snapshot under READ COMMITTED, and would miss a membership committed between
-- the read and this delete — which this statement removes but the caller would
-- then never evict.
-- name: PurgeUserMemberships :many
DELETE FROM public.users_teams
WHERE user_id = sqlc.arg(user_id)::uuid
RETURNING team_id;

-- name: PurgeUserAccessTokens :exec
DELETE FROM public.access_tokens
WHERE user_id = sqlc.arg(user_id)::uuid;
