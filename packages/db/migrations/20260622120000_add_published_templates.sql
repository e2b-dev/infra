-- +goose Up
-- +goose NO TRANSACTION

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public.env_defaults (
    env_id TEXT PRIMARY KEY REFERENCES public.envs(id),
    description TEXT
);

CREATE TABLE IF NOT EXISTS public.published_templates (
    id TEXT PRIMARY KEY,
    env_id TEXT NOT NULL REFERENCES public.envs(id) ON DELETE RESTRICT,
    is_agent BOOLEAN NOT NULL DEFAULT false,
    name TEXT NOT NULL,
    description TEXT NULL,
    icon TEXT NULL,
    template_ref TEXT NULL,
    build_pin_type TEXT NOT NULL DEFAULT 'default' CHECK (build_pin_type IN ('default', 'tag', 'build')),
    build_tag TEXT NULL,
    build_id UUID NULL REFERENCES public.env_builds(id) ON DELETE SET NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    unpublished_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT published_templates_build_pin_check CHECK (
        (build_pin_type = 'default' AND build_tag IS NULL AND build_id IS NULL)
        OR (build_pin_type = 'tag' AND build_tag IS NOT NULL AND build_id IS NULL)
        OR (build_pin_type = 'build' AND build_tag IS NULL AND build_id IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS public.published_template_agents (
    published_template_id TEXT PRIMARY KEY REFERENCES public.published_templates(id) ON DELETE CASCADE,
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

CREATE INDEX CONCURRENTLY IF NOT EXISTS published_templates_sort_idx
    ON public.published_templates (sort_order, id)
    WHERE unpublished_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS published_templates_agent_idx
    ON public.published_templates (sort_order, id)
    WHERE is_agent AND unpublished_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS published_templates_name_idx
    ON public.published_templates (name);

CREATE INDEX CONCURRENTLY IF NOT EXISTS published_templates_env_id_idx
    ON public.published_templates (env_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS published_templates_build_id_idx
    ON public.published_templates (build_id)
    WHERE build_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS published_template_agents_sort_idx
    ON public.published_template_agents (sort_order, published_template_id);

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS public.published_template_agents_sort_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.published_templates_build_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.published_templates_env_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.published_templates_name_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.published_templates_agent_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.published_templates_sort_idx;

-- +goose StatementBegin
DROP TABLE IF EXISTS public.published_template_agents;
DROP TABLE IF EXISTS public.published_templates;
-- +goose StatementEnd
