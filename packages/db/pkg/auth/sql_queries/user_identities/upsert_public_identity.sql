-- name: UpsertPublicIdentity :exec
INSERT INTO public.user_identities (oidc_iss, oidc_sub, user_id)
VALUES (sqlc.arg(oidc_iss)::text, sqlc.arg(oidc_sub)::text, sqlc.arg(user_id)::uuid)
ON CONFLICT (oidc_iss, oidc_sub)
DO UPDATE SET
    user_id = EXCLUDED.user_id,
    updated_at = now();
