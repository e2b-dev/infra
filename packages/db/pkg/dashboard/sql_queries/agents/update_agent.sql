-- name: UpdateAgent :one
UPDATE public.agents
SET
    name = CASE WHEN sqlc.arg(name_set)::boolean THEN sqlc.arg(name)::text ELSE name END,
    template_id = CASE WHEN sqlc.arg(template_id_set)::boolean THEN sqlc.arg(template_id)::text ELSE template_id END,
    description = CASE WHEN sqlc.arg(description_set)::boolean THEN sqlc.arg(description)::text ELSE description END,
    command = CASE WHEN sqlc.arg(command_set)::boolean THEN sqlc.narg(command)::text ELSE command END,
    author = CASE WHEN sqlc.arg(author_set)::boolean THEN sqlc.narg(author)::text ELSE author END,
    metadata = CASE WHEN sqlc.arg(metadata_set)::boolean THEN COALESCE(sqlc.narg(metadata)::text::jsonb, '{}'::jsonb) ELSE metadata END,
    public = CASE WHEN sqlc.arg(public_set)::boolean THEN sqlc.arg(public)::boolean ELSE public END,
    published_at = CASE WHEN sqlc.arg(published_at_set)::boolean THEN sqlc.narg(published_at)::timestamptz ELSE published_at END,
    updated_at = now()
WHERE id = sqlc.arg(id)::uuid
  AND team_id = sqlc.arg(team_id)::uuid
  AND deleted_at IS NULL
RETURNING
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
    deleted_at;
