-- name: DeleteAgent :execrows
UPDATE public.agents
SET
    deleted_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)::uuid
  AND team_id = sqlc.arg(team_id)::uuid
  AND deleted_at IS NULL;
