-- name: CreateCluster :exec
INSERT INTO public.clusters (
    id,
    name,
    endpoint,
    endpoint_tls,
    token,
    sandbox_proxy_domain,
    auth_org_id
)
VALUES (
    sqlc.arg(cluster_id)::uuid,
    sqlc.arg(name)::text,
    sqlc.arg(endpoint)::text,
    sqlc.arg(endpoint_tls)::boolean,
    sqlc.arg(token)::text,
    sqlc.narg(sandbox_proxy_domain)::text,
    sqlc.narg(auth_org_id)::text
);

-- name: AssignTeamCluster :execrows
UPDATE public.teams
SET cluster_id = sqlc.arg(cluster_id)::uuid
WHERE id = sqlc.arg(team_id)::uuid;
