-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public.users (
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    id uuid NOT NULL,
    email text NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY (id) REFERENCES auth.users(id) ON DELETE CASCADE,
    UNIQUE (email)
);

-- Enable row level security
ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;

-- Grant INSERT permission to trigger_user
GRANT INSERT ON public.users TO trigger_user;
CREATE POLICY "Allow to create a new user"
    ON public.users
    AS PERMISSIVE
    FOR INSERT
    TO trigger_user
    WITH CHECK (TRUE);

-- Grant UPDATE permission to trigger_user
-- We need to grant SELECT permission to trigger_user as well, so it can filter by id
GRANT SELECT (id) ON public.users TO trigger_user;
CREATE POLICY "Allow to select a user"
    ON public.users
    AS PERMISSIVE
    FOR SELECT
    TO trigger_user
    USING (true);

GRANT UPDATE ON public.users TO trigger_user;
CREATE POLICY "Allow to update a user"
    ON public.users
    AS PERMISSIVE
    FOR UPDATE
    TO trigger_user
    USING (true)
    WITH CHECK (true);

-- Create trigger function to update data from auth.users to public.users
CREATE OR REPLACE FUNCTION public.sync_insert_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO public.users (id, email)
    VALUES (NEW.id, NEW.email);

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- Create trigger function to update data from auth.users to public.users
CREATE OR REPLACE FUNCTION public.sync_update_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    UPDATE public.users
    SET email = NEW.email,
        updated_at = now()
    WHERE id = NEW.id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'User with id % does not exist in public.users', NEW.id;
    END IF;

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- Set function owner to trigger_user
ALTER FUNCTION public.sync_insert_auth_users_to_public_users_trigger() OWNER TO trigger_user;
ALTER FUNCTION public.sync_update_auth_users_to_public_users_trigger() OWNER TO trigger_user;

-- Create trigger on auth.users to copy data to public.users
CREATE OR REPLACE TRIGGER sync_inserts_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_insert_auth_users_to_public_users_trigger();


-- Create trigger on auth.users to copy data to public.users
CREATE OR REPLACE TRIGGER sync_updates_to_public_users
    AFTER UPDATE ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_update_auth_users_to_public_users_trigger();

-- Copy existing data from auth.users to public.users
INSERT INTO public.users (id, email)
SELECT id, email FROM auth.users;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.users;

DROP TRIGGER IF EXISTS sync_inserts_to_public_users ON auth.users;
DROP TRIGGER IF EXISTS sync_updates_to_public_users ON auth.users;

DROP FUNCTION IF EXISTS public.sync_insert_auth_users_to_public_users_trigger();
DROP FUNCTION IF EXISTS public.sync_update_auth_users_to_public_users_trigger();
-- +goose StatementEnd
