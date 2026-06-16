-- name: ListAgents :many
SELECT
    id,
    team_id,
    name,
    template_id,
    description,
    command,
    author,
    metadata,
    public,
    published_at,
    created_at,
    updated_at,
    deleted_at
FROM public.agents
WHERE deleted_at IS NULL
  AND published_at IS NOT NULL
  AND ((team_id IS NULL AND public = TRUE) OR team_id = sqlc.arg(team_id)::uuid)
ORDER BY position ASC, name ASC, id ASC;
