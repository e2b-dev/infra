-- Cluster-local teardown of one user's state, for the management interface's
-- purge route. public.users is deliberately left standing.

-- Read before the delete: afterwards nothing says which teams cached the user.
-- name: ListUserTeamIDs :many
SELECT team_id FROM public.users_teams
WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: PurgeUserMemberships :exec
DELETE FROM public.users_teams
WHERE user_id = sqlc.arg(user_id)::uuid;

-- name: PurgeUserAccessTokens :exec
DELETE FROM public.access_tokens
WHERE user_id = sqlc.arg(user_id)::uuid;
