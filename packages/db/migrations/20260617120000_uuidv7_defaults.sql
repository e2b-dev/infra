-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.uuidv7() RETURNS uuid AS $func$
DECLARE
    unix_ms bigint;
    rand text;
BEGIN
    unix_ms := floor(extract(epoch FROM clock_timestamp()) * 1000);
    rand := replace(gen_random_uuid()::text, '-', '');

    RETURN (
        lpad(to_hex(unix_ms), 12, '0') ||
        '7' ||
        substr(rand, 14, 3) ||
        substr('89ab', (get_byte(decode(substr(rand, 17, 2), 'hex'), 0) % 4) + 1, 1) ||
        substr(rand, 18, 15)
    )::uuid;
END;
$func$ LANGUAGE plpgsql VOLATILE;

CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid AS $func$
BEGIN
    RETURN uuidv7();
END;
$func$ LANGUAGE plpgsql;

ALTER TABLE IF EXISTS auth.users ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.teams ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.env_builds ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.snapshots ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.team_api_keys ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.access_tokens ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.clusters ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.env_aliases ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.users_teams ALTER COLUMN uuid_id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.volumes ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.addons ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE IF EXISTS public.env_build_assignments ALTER COLUMN id SET DEFAULT uuidv7();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- +goose StatementEnd
