-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public.agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id UUID REFERENCES public.teams(id) ON UPDATE NO ACTION ON DELETE CASCADE,
    name TEXT NOT NULL,
    template_id TEXT NOT NULL,
    description TEXT NOT NULL,
    command TEXT,
    author TEXT,
    public BOOLEAN NOT NULL DEFAULT FALSE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS agents_list_idx
    ON public.agents (team_id, public, deleted_at, position, name, id);

CREATE UNIQUE INDEX IF NOT EXISTS agents_public_template_id_idx
    ON public.agents (template_id)
    WHERE team_id IS NULL AND public = TRUE AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS agents_team_template_id_idx
    ON public.agents (team_id, template_id)
    WHERE team_id IS NOT NULL AND deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS public.agents;

-- +goose StatementEnd
