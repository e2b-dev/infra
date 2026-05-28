-- name: GetUserIdentitiesByUserIDs :many
SELECT oidc_iss, oidc_sub, user_id
FROM public.user_identities
WHERE oidc_iss = sqlc.arg(oidc_iss)::text
  AND user_id = ANY(sqlc.arg(user_ids)::uuid[]);
