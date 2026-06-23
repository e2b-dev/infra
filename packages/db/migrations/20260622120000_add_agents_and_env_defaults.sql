-- +goose Up
-- +goose NO TRANSACTION

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public.env_defaults (
    env_id TEXT PRIMARY KEY REFERENCES public.envs(id),
    description TEXT
);

CREATE TABLE IF NOT EXISTS public.agents (
    alias_id UUID PRIMARY KEY REFERENCES public.env_aliases(id) ON DELETE CASCADE,
    command TEXT NOT NULL,
    agent_name TEXT NULL,
    agent_description TEXT NULL,
    agent_icon TEXT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS agents_sort_idx
    ON public.agents (sort_order, alias_id);

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS public.agents_sort_idx;

-- +goose StatementBegin
DROP TABLE IF EXISTS public.agents;
-- +goose StatementEnd
