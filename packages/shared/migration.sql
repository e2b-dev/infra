CREATE SCHEMA IF NOT EXISTS auth;

-- Create RLS policies for user management
DO $$
    BEGIN
        BEGIN
            IF NOT EXISTS (
                SELECT 1
                FROM pg_roles
                WHERE rolname = 'authenticated'
            ) THEN
                EXECUTE 'CREATE ROLE authenticated;';
            END IF;
        END;
    END $$;
;

-- Create RLS policies for user management
DO $$
    BEGIN
        IF NOT EXISTS (
            SELECT 1
            FROM pg_proc p
                     JOIN pg_namespace n ON p.pronamespace = n.oid
            WHERE p.proname = 'uid' AND n.nspname = 'auth'
        ) THEN
            EXECUTE 'CREATE FUNCTION auth.uid() RETURNS uuid AS $func$
        BEGIN
            RETURN gen_random_uuid();
        END;
        $func$ LANGUAGE plpgsql;';
        END IF;
    END;
$$;


-- Create "users" table
CREATE TABLE IF NOT EXISTS "auth"."users"
(
    "id"                   uuid              NOT NULL DEFAULT gen_random_uuid(),
    "email"                text              NOT NULL,
    PRIMARY KEY ("id")
);
-- Add new schema named "auth"
CREATE SCHEMA IF NOT EXISTS "auth";
CREATE SCHEMA IF NOT EXISTS "extensions";
-- Create "tiers" table
CREATE TABLE IF NOT EXISTS "public"."tiers"
(
    "id"                   text   NOT NULL,
    "name"                 text   NOT NULL,
    "vcpu"                 bigint NOT NULL default '2'::bigint,
    "ram_mb"               bigint NOT NULL DEFAULT '512'::bigint,
    "disk_mb"              bigint NOT NULL DEFAULT '512'::bigint,
    "concurrent_instances" bigint NOT NULL,
    PRIMARY KEY ("id"),
    constraint tiers_concurrent_sessions_check check ((concurrent_instances > 0)),
    constraint tiers_disk_mb_check check ((disk_mb > 0)),
    constraint tiers_ram_mb_check check ((ram_mb > 0)),
    constraint tiers_vcpu_check check ((vcpu > 0))
);
ALTER TABLE "public"."tiers" ENABLE ROW LEVEL SECURITY;


COMMENT ON COLUMN public.tiers.concurrent_instances
    IS 'The number of instances the team can run concurrently';

-- Create "teams" table
CREATE TABLE IF NOT EXISTS "public"."teams"
(
    "id"         uuid                 DEFAULT gen_random_uuid(),
    "created_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "is_default" boolean     NOT NULL,
    "is_blocked" boolean     NOT NULL DEFAULT FALSE,
    "name"       text        NOT NULL,
    "tier"       text        NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "teams_tiers_teams" FOREIGN KEY ("tier") REFERENCES "public"."tiers" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
ALTER TABLE "public"."teams" ENABLE ROW LEVEL SECURITY;

-- Create "envs" table
CREATE TABLE IF NOT EXISTS "public"."envs"
(
    "id"              text        NOT NULL,
    "created_at"      timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at"      timestamptz NOT NULL,
    "dockerfile"      text        NOT NULL,
    "public"          boolean     NOT NULL DEFAULT FALSE,
    "build_id"        uuid        NOT NULL,
    "build_count"     integer     NOT NULL DEFAULT 1,
    "spawn_count"     bigint      NOT NULL DEFAULT '0'::bigint,
    "last_spawned_at" timestamptz NULL,
    "team_id"         uuid        NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "envs_teams_envs" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
ALTER TABLE "public"."envs" ENABLE ROW LEVEL SECURITY;

COMMENT ON COLUMN public.envs.last_spawned_at
    IS 'Timestamp of the last time the env was spawned';
COMMENT ON COLUMN public.envs.spawn_count
    IS 'Number of times the env was spawned';

-- Create "env_aliases" table
CREATE TABLE IF NOT EXISTS "public"."env_aliases"
(
    "alias"   text    NOT NULL,
    "is_name" boolean NOT NULL DEFAULT true,
    "env_id"  text    NULL,
    PRIMARY KEY ("alias"),
    CONSTRAINT "env_aliases_envs_env_aliases" FOREIGN KEY ("env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
ALTER TABLE "public"."env_aliases" ENABLE ROW LEVEL SECURITY;

-- Create "team_api_keys" table
CREATE TABLE IF NOT EXISTS "public"."team_api_keys"
(
    "api_key"    character varying(44) NOT NULL,
    "created_at" timestamptz           NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "team_id"    uuid                  NOT NULL,
    PRIMARY KEY ("api_key"),
    CONSTRAINT "team_api_keys_teams_team_api_keys" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
ALTER TABLE "public"."team_api_keys" ENABLE ROW LEVEL SECURITY;

-- Create "users" table
CREATE TABLE IF NOT EXISTS "auth"."users"
(
    "id"    uuid                   NOT NULL DEFAULT gen_random_uuid(),
    "email" character varying(255) NOT NULL,
    PRIMARY KEY ("id")
);

-- Create "access_tokens" table
CREATE TABLE IF NOT EXISTS "public"."access_tokens"
(
    "access_token" text        NOT NULL,
    "user_id"      uuid        NOT NULL,
    "created_at"   timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY ("access_token"),
    CONSTRAINT "access_tokens_users_access_tokens" FOREIGN KEY ("user_id") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
ALTER TABLE "public"."access_tokens" ENABLE ROW LEVEL SECURITY;

-- Create "users_teams" table
CREATE TABLE IF NOT EXISTS "public"."users_teams"
(
    "id"      bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY,
    "user_id" uuid   NOT NULL,
    "team_id" uuid   NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "users_teams_teams_teams" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "users_teams_users_users" FOREIGN KEY ("user_id") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
ALTER TABLE "public"."users_teams" ENABLE ROW LEVEL SECURITY;

-- Create RLS policies
DO $$
BEGIN
    BEGIN
        CREATE POLICY IF NOT EXISTS "Allow selection for users that are in the team"
            ON "public"."teams"
            AS PERMISSIVE
            FOR SELECT
            TO authenticated
            USING ((auth.uid() IN ( SELECT users_teams.user_id
                                    FROM users_teams
                                    WHERE (users_teams.team_id = teams.id))));

        CREATE POLICY "Enable select for users in relevant team"
            ON "public"."users_teams"
            AS PERMISSIVE
            FOR SELECT
            TO authenticated
            USING ((auth.uid() = user_id));

        CREATE POLICY "Enable select for users based on user_id"
            ON public.access_tokens
            AS PERMISSIVE
            FOR SELECT
            TO authenticated
            USING ((auth.uid() = user_id));


        CREATE POLICY "Allow selection for users that are in the team"
            ON "public"."team_api_keys"
            AS PERMISSIVE
            FOR SELECT
            TO authenticated
            USING ((auth.uid() IN ( SELECT users_teams.user_id
                                    FROM users_teams
                                    WHERE (users_teams.team_id = team_api_keys.team_id))));
    EXCEPTION WHEN undefined_function
        THEN RAISE NOTICE 'Policy were not created, probably because the function auth.uid() does not exist.';
    END;
END $$;

-- Create index "usersteams_team_id_user_id" to table: "users_teams"
CREATE UNIQUE INDEX "usersteams_team_id_user_id" ON "public"."users_teams" ("team_id", "user_id");
-- Add base tier
INSERT INTO public.tiers (id, name, vcpu, ram_mb, disk_mb, concurrent_instances) VALUES ('base_v1', 'Base tier', 2, 512, 512, 20);

-- Create user for triggers
CREATE USER trigger_user;
GRANT trigger_user TO postgres;

GRANT CREATE, USAGE ON SCHEMA public TO trigger_user;
GRANT USAGE ON SCHEMA extensions TO trigger_user;
GRANT USAGE ON SCHEMA auth TO trigger_user;

GRANT SELECT, INSERT, TRIGGER ON public.teams TO trigger_user;
GRANT INSERT ON public.users_teams TO trigger_user;
GRANT INSERT ON public.team_api_keys TO trigger_user;
GRANT INSERT ON public.access_tokens TO trigger_user;

--
CREATE OR REPLACE FUNCTION public.generate_default_team_trigger()
    RETURNS TRIGGER
    LANGUAGE plpgsql
    AS $create_default_team$
DECLARE
    team_id                 uuid;
BEGIN
    RAISE NOTICE 'Creating default team for user %', NEW.id;
    INSERT INTO public.teams(name, is_default, tier, email) VALUES (NEW.email, true, 'base_v1', NEW.email) RETURNING id INTO team_id;
    INSERT INTO public.users_teams(user_id, team_id) VALUES (NEW.id, team_id);
    RAISE NOTICE 'Created default team for user % and team %', NEW.id, team_id;
    RETURN NEW;
END
$create_default_team$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.generate_default_team_trigger() OWNER TO trigger_user;

CREATE OR REPLACE TRIGGER create_default_team
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION generate_default_team_trigger();


CREATE OR REPLACE FUNCTION public.generate_teams_api_keys_trigger() RETURNS TRIGGER
    LANGUAGE plpgsql
AS $generate_teams_api_keys$
DECLARE
    key_prefix TEXT := 'e2b_';
    generated_key TEXT;
BEGIN
    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    generated_key := encode(extensions.gen_random_bytes(20), 'hex');
    INSERT INTO public.team_api_keys (team_id, api_key)
    VALUES (NEW.id, key_prefix || generated_key);
    RETURN NEW;
END
$generate_teams_api_keys$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.generate_teams_api_keys_trigger() OWNER TO trigger_user;

CREATE OR REPLACE TRIGGER team_api_keys_trigger
    AFTER INSERT ON public.teams
    FOR EACH ROW EXECUTE FUNCTION generate_teams_api_keys_trigger();



CREATE OR REPLACE FUNCTION public.generate_access_token_trigger() RETURNS TRIGGER
    LANGUAGE plpgsql
    AS $generate_access_token$
DECLARE
    key_prefix TEXT := 'sk_e2b_';
    generated_key TEXT;
BEGIN
    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    generated_key := encode(extensions.gen_random_bytes(20), 'hex');
    INSERT INTO public.access_tokens (user_id, access_token)
    VALUES (NEW.id, key_prefix || generated_key);
    RETURN NEW;
END;
$generate_access_token$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.generate_access_token_trigger() OWNER TO trigger_user;


CREATE OR REPLACE TRIGGER create_access_token
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION generate_access_token_trigger();


CREATE POLICY "Allow to create an access token to new user"
    ON public.access_tokens
    AS PERMISSIVE
    FOR INSERT
    TO trigger_user
    WITH CHECK (TRUE);

CREATE POLICY "Allow to create a team to new user"
    ON public.teams
    AS PERMISSIVE
    FOR INSERT
    TO trigger_user
    WITH CHECK (TRUE);

CREATE POLICY "Allow to create a user team connection to new user"
    ON public.users_teams
    AS PERMISSIVE
    FOR INSERT
    TO trigger_user
    WITH CHECK (TRUE);

CREATE POLICY "Allow to select a team for supabase auth admin"
    ON public.teams
    AS PERMISSIVE
    FOR SELECT
    TO trigger_user
    USING (TRUE);

CREATE POLICY "Allow to create a team api key to new user"
    ON public.team_api_keys
    AS PERMISSIVE
    FOR INSERT
    TO trigger_user
    WITH CHECK (TRUE);
-- Modify "envs" table
ALTER TABLE "public"."envs" ADD COLUMN "vcpu" bigint NOT NULL, ADD COLUMN "ram_mb" bigint NOT NULL, ADD COLUMN "free_disk_size_mb" bigint NOT NULL, ADD COLUMN "total_disk_size_mb" bigint NOT NULL;
-- Modify "teams" table
ALTER TABLE "public"."teams" ADD COLUMN "email" character varying(255) NULL;


CREATE OR REPLACE FUNCTION public.generate_default_team() RETURNS TRIGGER
    LANGUAGE plpgsql
AS $create_default_team$
DECLARE
    team_id                 uuid;
BEGIN
    INSERT INTO public.teams(name, is_default, tier, email) VALUES (NEW.email, true, 'base', NEW.email) RETURNING id INTO team_id;
    INSERT INTO public.users_teams(user_id, team_id) VALUES (NEW.id, team_id);
    RAISE NOTICE 'Created default team for user % and team %', NEW.id, team_id;
    RETURN NEW;
END
$create_default_team$ SECURITY DEFINER SET search_path = public;

UPDATE "public"."teams" SET "email" = "name";

ALTER TABLE "public"."teams" ALTER COLUMN "email" SET NOT NULL;-- Modify "teams" table
ALTER TABLE "public"."teams" ADD COLUMN "is_banned" boolean NOT NULL DEFAULT false, ADD COLUMN "blocked_reason" TEXT NULL;

-- Modify "envs" table
ALTER TABLE "public"."envs" ADD COLUMN "kernel_version" character varying NULL;
UPDATE  "public"."envs" SET "kernel_version" = 'vmlinux-5.10.186-old';
ALTER TABLE "public"."envs" ALTER COLUMN "kernel_version" SET NOT NULL;
ALTER TABLE "public"."envs" ALTER COLUMN "kernel_version" SET DEFAULT 'vmlinux-5.10.186';
-- Modify "tiers" table
ALTER TABLE "public"."tiers" ADD COLUMN "max_length_hours" bigint NULL;
UPDATE "public"."tiers" SET "max_length_hours" = 1;
ALTER TABLE "public"."tiers" ALTER COLUMN "max_length_hours" SET NOT NULL;
-- Modify "envs" table
ALTER TABLE "public"."envs" ADD COLUMN "firecracker_version" character varying NOT NULL DEFAULT 'v1.5.0_8a43b32e';-- Modify "envs" table
ALTER TABLE "public"."envs" ALTER COLUMN "firecracker_version" SET DEFAULT 'v1.7.0-dev_8bb88311';
-- Modify "tiers" table
ALTER TABLE "public"."tiers" DROP CONSTRAINT "tiers_ram_mb_check", DROP CONSTRAINT "tiers_vcpu_check", DROP COLUMN "vcpu", DROP COLUMN "ram_mb";
-- Modify "env_aliases" table
ALTER TABLE "public"."env_aliases" RENAME COLUMN "is_name" TO "is_renamable";
ALTER TABLE "public"."env_aliases" ALTER COLUMN "env_id" SET NOT NULL;

-- Create "env_builds" table
CREATE TABLE "public"."env_builds" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP, "updated_at" timestamptz NOT NULL, "finished_at" timestamptz NULL, "status" text NOT NULL DEFAULT 'waiting', "dockerfile" text NULL, "start_cmd" text NULL, "vcpu" bigint NOT NULL, "ram_mb" bigint NOT NULL, "free_disk_size_mb" bigint NOT NULL, "total_disk_size_mb" bigint NULL, "kernel_version" text NOT NULL DEFAULT 'vmlinux-5.10.186', "firecracker_version" text NOT NULL DEFAULT 'v1.7.0-dev_8bb88311', "env_id" text NULL, PRIMARY KEY ("id"), CONSTRAINT "env_builds_envs_builds" FOREIGN KEY ("env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
ALTER TABLE "public"."env_builds" ENABLE ROW LEVEL SECURITY;

-- Populate "env_builds" table
INSERT INTO "public"."env_builds"(updated_at, finished_at, status, dockerfile, start_cmd, vcpu, ram_mb, free_disk_size_mb, total_disk_size_mb, kernel_version, firecracker_version, env_id)
SELECT CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'success', dockerfile, NULL, vcpu, ram_mb, free_disk_size_mb, total_disk_size_mb, kernel_version, firecracker_version, id
FROM "public"."envs";

-- Modify "envs" table
ALTER TABLE "public"."envs" DROP COLUMN "dockerfile", DROP COLUMN "build_id", DROP COLUMN "vcpu", DROP COLUMN "ram_mb", DROP COLUMN "free_disk_size_mb", DROP COLUMN "total_disk_size_mb", DROP COLUMN "kernel_version", DROP COLUMN "firecracker_version";
DROP TRIGGER create_default_team ON auth.users;
DROP FUNCTION generate_default_team_trigger();
DROP TRIGGER team_api_keys_trigger ON public.teams;
DROP FUNCTION generate_teams_api_keys_trigger();
DROP TRIGGER create_access_token ON auth.users;
DROP FUNCTION generate_access_token_trigger();

CREATE OR REPLACE FUNCTION public.extra_for_post_user_signup(user_id uuid, team_id uuid)
    RETURNS void
    LANGUAGE plpgsql
AS $extra_for_post_user_signup$
DECLARE
BEGIN
END
$extra_for_post_user_signup$ SECURITY DEFINER SET search_path = public;

CREATE OR REPLACE FUNCTION public.generate_team_api_key()
    RETURNS TEXT
    LANGUAGE plpgsql
AS $generate_team_api_key$
DECLARE
    team_api_key_prefix TEXT := 'e2b_';
    generated_key TEXT;
BEGIN
    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    generated_key := encode(extensions.gen_random_bytes(20), 'hex');
    RETURN team_api_key_prefix || generated_key;
END
$generate_team_api_key$ SECURITY DEFINER SET search_path = public;

ALTER TABLE public.team_api_keys ALTER COLUMN api_key SET DEFAULT public.generate_team_api_key();

CREATE OR REPLACE FUNCTION public.generate_access_token()
    RETURNS TEXT
    LANGUAGE plpgsql
AS $extra_for_post_user_signup$
DECLARE
    access_token_prefix TEXT := 'sk_e2b_';
    generated_token TEXT;
BEGIN
    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    generated_token := encode(extensions.gen_random_bytes(20), 'hex');
    RETURN access_token_prefix || generated_token;
END
$extra_for_post_user_signup$ SECURITY DEFINER SET search_path = public;

ALTER TABLE public.access_tokens ALTER COLUMN access_token SET DEFAULT public.generate_access_token();

ALTER FUNCTION public.extra_for_post_user_signup(uuid, uuid) OWNER TO trigger_user;

CREATE OR REPLACE FUNCTION public.post_user_signup()
    RETURNS TRIGGER
    LANGUAGE plpgsql
AS $post_user_signup$
DECLARE
    team_id                 uuid;
BEGIN
    RAISE NOTICE 'Creating default team for user %', NEW.id;
    INSERT INTO public.teams(name, is_default, tier, email) VALUES (NEW.email, true, 'base_v1', NEW.email) RETURNING id INTO team_id;
    INSERT INTO public.users_teams(user_id, team_id) VALUES (NEW.id, team_id);
    RAISE NOTICE 'Created default team for user % and team %', NEW.id, team_id;

    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    INSERT INTO public.team_api_keys (team_id)
    VALUES (team_id);

    INSERT INTO public.access_tokens (user_id)
    VALUES (NEW.id);

    PERFORM public.extra_for_post_user_signup(NEW.id, team_id);

    RETURN NEW;
END
$post_user_signup$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.post_user_signup() OWNER TO trigger_user;


CREATE OR REPLACE TRIGGER post_user_signup
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION post_user_signup();


CREATE OR REPLACE FUNCTION is_member_of_team(_user_id uuid, _team_id uuid) RETURNS bool AS $$
SELECT EXISTS (
    SELECT 1
    FROM public.users_teams ut
    WHERE ut.user_id = _user_id
      AND ut.team_id = _team_id
);
$$ LANGUAGE sql SECURITY DEFINER;

-- Create RLS policies for user management
DO $$
    BEGIN
        BEGIN
            CREATE POLICY "Allow users to delete a team api key"
                ON "public"."team_api_keys"
                AS PERMISSIVE
                FOR DELETE
                TO authenticated
                USING ((SELECT auth.uid()) IN ( SELECT users_teams.user_id
                    FROM users_teams
                    WHERE (users_teams.team_id = team_api_keys.team_id)));

            CREATE POLICY "Allow users to create a new team user entry"
                ON "public"."users_teams"
                AS PERMISSIVE
                FOR INSERT
                TO authenticated
                WITH CHECK (team_id IN ( SELECT users_teams.team_id
                FROM users_teams
                WHERE (users_teams.user_id = (SELECT auth.uid()))));

            CREATE POLICY  "Allow users to delete a team user entry"
                ON public.users_teams
                AS PERMISSIVE
                FOR DELETE
                TO authenticated
                USING (team_id IN ( SELECT users_teams.team_id
                FROM users_teams
                WHERE (users_teams.user_id = auth.uid())));

            CREATE POLICY "Allow update for users that are in the team"
                ON "public"."teams"
                AS PERMISSIVE
                FOR UPDATE
                TO authenticated
                USING ((auth.uid() IN ( SELECT users_teams.user_id
                FROM users_teams
                WHERE (users_teams.team_id = teams.id))));

            ALTER POLICY "Enable select for users in relevant team"
                on "public"."users_teams"
                to authenticated
                using (is_member_of_team(auth.uid(), team_id)
            );

        END;
    END $$;
;-- Modify "env_builds" table
ALTER TABLE "public"."env_builds" ADD COLUMN "envd_version" text NULL;

-- Populate "envd_version" column
UPDATE "public"."env_builds" SET "envd_version" = 'v0.0.1';
-- Modify "access_tokens" table
ALTER TABLE "public"."users_teams" ADD COLUMN "is_default" boolean NOT NULL DEFAULT false;
UPDATE "public"."users_teams" ut SET "is_default" = t."is_default" FROM "public"."teams" t WHERE ut."team_id" = t."id";

CREATE OR REPLACE FUNCTION public.post_user_signup()
    RETURNS TRIGGER
    LANGUAGE plpgsql
AS $post_user_signup$
DECLARE
    team_id                 uuid;
BEGIN
    RAISE NOTICE 'Creating default team for user %', NEW.id;
    INSERT INTO public.teams(name, is_default, tier, email) VALUES (NEW.email, true, 'base_v1', NEW.email) RETURNING id INTO team_id;
    INSERT INTO public.users_teams(user_id, team_id, is_default) VALUES (NEW.id, team_id, true);
    RAISE NOTICE 'Created default team for user % and team %', NEW.id, team_id;

    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    INSERT INTO public.team_api_keys (team_id)
    VALUES (team_id);

    INSERT INTO public.access_tokens (user_id)
    VALUES (NEW.id);

    PERFORM public.extra_for_post_user_signup(NEW.id, team_id);

    RETURN NEW;
END
$post_user_signup$ SECURITY DEFINER SET search_path = public;

ALTER FUNCTION public.post_user_signup() OWNER TO trigger_user;DROP POLICY "Allow update for users that are in the team" ON "public"."teams";
DROP POLICY "Allow users to delete a team user entry" ON "public"."users_teams";
DROP POLICY "Allow users to create a new team user entry" ON "public"."users_teams";
-- Modify "team_api_keys" table
ALTER TABLE "public"."team_api_keys"
    ADD COLUMN "updated_at" timestamptz NULL,
    ADD COLUMN "name" text NOT NULL DEFAULT 'Unnamed API Key',
    ADD COLUMN "last_used" timestamptz NULL,
    ADD COLUMN "created_by" uuid NULL,
    ADD CONSTRAINT "team_api_keys_users_created_api_keys" FOREIGN KEY ("created_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Modify "team_api_keys" table
ALTER TABLE "public"."team_api_keys"
    DROP CONSTRAINT "team_api_keys_pkey",
    ADD COLUMN "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    ADD PRIMARY KEY ("id");
-- Create index "team_api_keys_api_key_key" to table: "team_api_keys"
CREATE UNIQUE INDEX "team_api_keys_api_key_key" ON "public"."team_api_keys" ("api_key");-- Modify "envs" table
ALTER TABLE "public"."envs"
    ADD COLUMN "created_by" uuid NULL,
    ADD CONSTRAINT "envs_users_created_envs" FOREIGN KEY ("created_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Add column to "users_teams" table
ALTER TABLE "public"."users_teams" ADD COLUMN "added_by" uuid NULL;
ALTER TABLE "public"."users_teams" ADD CONSTRAINT "users_teams_added_by_user" FOREIGN KEY ("added_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;-- Create "snapshots" table
CREATE TABLE "public"."snapshots"
(
    created_at timestamp with time zone null,
    env_id text not null,
    sandbox_id text not null,
    id uuid not null default gen_random_uuid (),
    metadata jsonb null,
    base_env_id text not null,
    constraint snapshots_pkey primary key (id)
);
ALTER TABLE "public"."snapshots" ENABLE ROW LEVEL SECURITY;
-- Alter "teams" table
ALTER TABLE "public"."teams" DROP COLUMN "is_default";

CREATE OR REPLACE FUNCTION public.post_user_signup()
    RETURNS TRIGGER
    LANGUAGE plpgsql
AS $post_user_signup$
DECLARE
    team_id                 uuid;
BEGIN
    RAISE NOTICE 'Creating default team for user %', NEW.id;
    INSERT INTO public.teams(name, tier, email) VALUES (NEW.email, 'base_v1', NEW.email) RETURNING id INTO team_id;
    INSERT INTO public.users_teams(user_id, team_id, is_default) VALUES (NEW.id, team_id, true);
    RAISE NOTICE 'Created default team for user % and team %', NEW.id, team_id;

    -- Generate a random 20 byte string and encode it as hex, so it's 40 characters
    INSERT INTO public.team_api_keys (team_id)
    VALUES (team_id);

    INSERT INTO public.access_tokens (user_id)
    VALUES (NEW.id);

    PERFORM public.extra_for_post_user_signup(NEW.id, team_id);

    RETURN NEW;
END
$post_user_signup$ SECURITY DEFINER SET search_path = public;
ALTER TABLE "public"."snapshots"
    ADD CONSTRAINT "snapshots_envs_env_id" FOREIGN KEY ("env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    ADD CONSTRAINT "snapshots_envs_base_env_id" FOREIGN KEY ("base_env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;CREATE INDEX idx_envs_builds_envs ON public.env_builds (env_id);
CREATE INDEX idx_envs_envs_aliases ON public.env_aliases (env_id);
CREATE INDEX idx_users_access_tokens ON public.access_tokens (user_id);
CREATE INDEX idx_teams_envs ON public.envs (team_id);
CREATE INDEX idx_team_team_api_keys ON public.team_api_keys (team_id);
CREATE INDEX idx_teams_user_teams ON public.users_teams (team_id);
CREATE INDEX idx_users_user_teams ON public.users_teams (user_id);
