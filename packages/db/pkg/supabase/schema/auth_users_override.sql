CREATE SCHEMA IF NOT EXISTS auth;

CREATE ROLE authenticated;

CREATE FUNCTION auth.uid() RETURNS uuid AS $func$
BEGIN
    RETURN gen_random_uuid();
END;
$func$ LANGUAGE plpgsql;

-- Grant execute on auth.uid() to postgres role
GRANT EXECUTE ON FUNCTION auth.uid() TO postgres;

CREATE TABLE auth.users (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    email text NOT NULL,
    created_at timestamptz DEFAULT now(),
    raw_app_meta_data jsonb,
    PRIMARY KEY (id)
);
