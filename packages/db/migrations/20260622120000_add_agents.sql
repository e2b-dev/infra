-- +goose Up

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public.agent_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alias_id UUID NOT NULL REFERENCES public.env_aliases(id) ON DELETE CASCADE,
    command TEXT NOT NULL,
    name TEXT NULL,
    description TEXT NULL,
    icon TEXT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION public.set_agent_definitions_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS set_agent_definitions_updated_at ON public.agent_definitions;

CREATE TRIGGER set_agent_definitions_updated_at
    BEFORE UPDATE ON public.agent_definitions
    FOR EACH ROW
    EXECUTE FUNCTION public.set_agent_definitions_updated_at();

CREATE INDEX IF NOT EXISTS agent_definitions_sort_idx
    ON public.agent_definitions (sort_order, alias_id, id);

CREATE INDEX IF NOT EXISTS agent_definitions_alias_id_idx
    ON public.agent_definitions (alias_id);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TRIGGER IF EXISTS set_agent_definitions_updated_at ON public.agent_definitions;
DROP FUNCTION IF EXISTS public.set_agent_definitions_updated_at();
DROP TABLE IF EXISTS public.agent_definitions;
-- +goose StatementEnd
