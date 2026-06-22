-- +goose Up
-- +goose StatementBegin
-- env_defaults marks globally shared default templates (also created by the
-- dashboard migration set). Created here idempotently so it exists in every
-- environment the main API runs in, enabling GetTeamTemplatesWithCursor to
-- include default templates for non-BYOC teams.
CREATE TABLE IF NOT EXISTS public.env_defaults (
    env_id TEXT PRIMARY KEY REFERENCES public.envs(id),
    description TEXT
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- No-op: the dashboard migration set also owns this table, so we don't drop it.
SELECT 1;
-- +goose StatementEnd
