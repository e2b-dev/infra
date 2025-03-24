BEGIN;

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

COMMIT;
