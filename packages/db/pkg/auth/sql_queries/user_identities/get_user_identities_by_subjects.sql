-- name: GetUserIdentitiesBySubjects :many
SELECT oidc_iss, oidc_sub, user_id
FROM public.user_identities
WHERE oidc_iss = sqlc.arg(oidc_iss)::text
  AND oidc_sub = ANY(sqlc.arg(oidc_subs)::text[]);
