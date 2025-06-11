-- name: GetClustersWithTeams :many
SELECT sqlc.embed(t), sqlc.embed(c)
FROM public.teams t
INNER JOIN public.clusters AS c ON t.cluster_id = c.id;
