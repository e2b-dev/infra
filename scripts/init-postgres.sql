-- Create postgres user if it doesn't exist
-- This is needed because some migrations reference the postgres user
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'postgres') THEN
        CREATE USER postgres WITH SUPERUSER CREATEDB CREATEROLE LOGIN;
    END IF;
END
$$;

-- Grant all privileges on the database to postgres user
GRANT ALL PRIVILEGES ON DATABASE e2b TO postgres;

-- Create necessary schemas
CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS public;

-- Grant usage on schemas
GRANT USAGE ON SCHEMA auth TO postgres;
GRANT USAGE ON SCHEMA public TO postgres;

-- Grant all privileges on all tables in schemas
GRANT ALL ON ALL TABLES IN SCHEMA auth TO postgres;
GRANT ALL ON ALL TABLES IN SCHEMA public TO postgres;

-- Grant all privileges on all sequences in schemas
GRANT ALL ON ALL SEQUENCES IN SCHEMA auth TO postgres;
GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO postgres;

-- Grant all privileges on all functions in schemas
GRANT ALL ON ALL FUNCTIONS IN SCHEMA auth TO postgres;
GRANT ALL ON ALL FUNCTIONS IN SCHEMA public TO postgres; 