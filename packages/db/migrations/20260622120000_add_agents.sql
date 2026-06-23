-- +goose Up

-- +goose StatementBegin
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

CREATE OR REPLACE FUNCTION public.set_agents_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS set_agents_updated_at ON public.agents;

CREATE TRIGGER set_agents_updated_at
    BEFORE UPDATE ON public.agents
    FOR EACH ROW
    EXECUTE FUNCTION public.set_agents_updated_at();

CREATE INDEX IF NOT EXISTS agents_sort_idx
    ON public.agents (sort_order, alias_id);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TRIGGER IF EXISTS set_agents_updated_at ON public.agents;
DROP FUNCTION IF EXISTS public.set_agents_updated_at();
DROP TABLE IF EXISTS public.agents;
-- +goose StatementEnd
