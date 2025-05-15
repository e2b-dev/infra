-- +goose Up
-- +goose StatementBegin
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


-- Grant execute on auth.uid() to postgres role
GRANT EXECUTE ON FUNCTION auth.uid() TO postgres;

-- Check if the table exists before trying to create it
DO $$
    BEGIN
        IF NOT EXISTS (
            SELECT 1
            FROM information_schema.tables
            WHERE table_schema = 'auth'
              AND table_name = 'users'
        ) THEN
            EXECUTE '
        CREATE TABLE auth.users (
            id uuid NOT NULL DEFAULT gen_random_uuid(),
            email text NOT NULL,
            PRIMARY KEY (id)
        );';
        END IF;
    END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- +goose StatementEnd
