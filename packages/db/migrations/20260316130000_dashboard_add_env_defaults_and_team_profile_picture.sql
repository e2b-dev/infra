-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS public.env_defaults (
    env_id TEXT PRIMARY KEY REFERENCES public.envs(id),
    description TEXT
);

ALTER TABLE public.env_defaults ENABLE ROW LEVEL SECURITY;

CREATE INDEX IF NOT EXISTS env_defaults_env_id_idx ON public.env_defaults(env_id);

ALTER TABLE public.teams ADD COLUMN IF NOT EXISTS profile_picture_url TEXT;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE public.teams DROP COLUMN IF EXISTS profile_picture_url;
DROP INDEX IF EXISTS env_defaults_env_id_idx;
DROP TABLE IF EXISTS public.env_defaults;

-- +goose StatementEnd
