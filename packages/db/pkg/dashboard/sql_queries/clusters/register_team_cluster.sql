-- name: CreateCluster :one
INSERT INTO public.clusters (
    name,
    endpoint,
    endpoint_tls,
    token,
    sandbox_proxy_domain,
    auth_org_id
)
VALUES (
    sqlc.arg(name)::text,
    sqlc.arg(endpoint)::text,
    sqlc.arg(endpoint_tls)::boolean,
    sqlc.arg(token)::text,
    sqlc.narg(sandbox_proxy_domain)::text,
    sqlc.narg(auth_org_id)::text
)
RETURNING id;

-- name: DeleteCluster :execrows
DELETE FROM public.clusters
WHERE id = sqlc.arg(cluster_id)::uuid;

-- name: TeamClusterAssignment :one
SELECT cluster_id
FROM public.teams
WHERE id = sqlc.arg(team_id)::uuid
  AND cluster_id IS NOT NULL;

-- name: AssignTeamCluster :execrows
UPDATE public.teams
SET cluster_id = sqlc.arg(cluster_id)::uuid
WHERE id = sqlc.arg(team_id)::uuid;

-- name: DetachTeamCluster :one
WITH locked_team AS MATERIALIZED (
    SELECT cluster_id
    FROM public.teams
    WHERE id = sqlc.arg(team_id)::uuid
    FOR UPDATE
),
detached AS (
    UPDATE public.teams AS team
    SET cluster_id = NULL
    FROM locked_team
    WHERE team.id = sqlc.arg(team_id)::uuid
      AND locked_team.cluster_id = sqlc.arg(cluster_id)::uuid
    RETURNING TRUE
)
SELECT locked_team.cluster_id,
       EXISTS (SELECT FROM detached) AS detached
FROM locked_team;
