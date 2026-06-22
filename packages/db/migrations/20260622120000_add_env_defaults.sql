-- +goose Up
-- +goose StatementBegin
-- env_defaults marks globally shared default templates (also created by the
-- dashboard migration set). Created here idempotently so it exists in every
-- environment the main API runs in, enabling GetTeamTemplatesWithCursor to
-- include default templates for non-BYOC teams.
CREATE TABLE IF NOT EXISTS public.env_defaults (
    env_id TEXT PRIMARY KEY REFERENCES public.envs(id) ON DELETE CASCADE,
    description TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Ensure ON DELETE CASCADE even where the table pre-existed without it (the
-- dashboard migration created the FK without a cascade). Templates are
-- hard-deleted from public.envs, so a non-cascading FK would block deleting any
-- template that is also a default.
ALTER TABLE public.env_defaults DROP CONSTRAINT IF EXISTS env_defaults_env_id_fkey;
ALTER TABLE public.env_defaults
    ADD CONSTRAINT env_defaults_env_id_fkey
    FOREIGN KEY (env_id) REFERENCES public.envs(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- No-op: the dashboard migration set also owns this table, so we don't drop it.
SELECT 1;
-- +goose StatementEnd
