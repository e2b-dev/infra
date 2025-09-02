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

-- Skip table creation if you are in Supabase environment where auth.users table already exists
DO $$
    BEGIN
        IF EXISTS (
            SELECT 1
            FROM information_schema.tables
            WHERE table_schema = 'auth'
              AND table_name = 'users'
        ) THEN RETURN;
        END IF;
    END;
$$;

CREATE TABLE auth.users (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    email text NOT NULL,
    PRIMARY KEY (id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- +goose StatementEnd
