-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS public.users (
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

-- Create trigger function to copy data from auth.users to public.users
CREATE OR REPLACE FUNCTION public.sync_auth_users_to_public_users_trigger() RETURNS TRIGGER
LANGUAGE plpgsql
AS $func$
BEGIN
    INSERT INTO public.users (id, email)
    VALUES (NEW.id, NEW.email);

    RETURN NEW;
END;
$func$ SECURITY DEFINER SET search_path = public;

-- Set function owner to trigger_user
ALTER FUNCTION public.sync_auth_users_to_public_users_trigger() OWNER TO trigger_user;

-- Create trigger on auth.users to copy data to public.users
CREATE OR REPLACE TRIGGER sync_to_public_users
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.sync_auth_users_to_public_users_trigger();

-- Copy existing data from auth.users to public.users
INSERT INTO public.users (id, email)
SELECT id, email FROM auth.users;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sync_to_public_users ON auth.users;
DROP FUNCTION IF EXISTS public.sync_auth_users_to_public_users();
DROP TABLE IF EXISTS public.users;
-- +goose StatementEnd
