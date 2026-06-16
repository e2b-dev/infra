-- name: CreateAgent :one
INSERT INTO public.agents (
    team_id,
    name,
    template_id,
    description,
    command,
    author,
    metadata,
    public,
    published_at
)
VALUES (
    sqlc.arg(team_id)::uuid,
    sqlc.arg(name)::text,
    sqlc.arg(template_id)::text,
    sqlc.arg(description)::text,
    sqlc.narg(command)::text,
    sqlc.narg(author)::text,
    COALESCE(sqlc.narg(metadata)::text::jsonb, '{}'::jsonb),
    sqlc.arg(public)::boolean,
    sqlc.narg(published_at)::timestamptz
)
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
