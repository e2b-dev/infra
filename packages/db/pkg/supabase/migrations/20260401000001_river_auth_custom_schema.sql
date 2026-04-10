-- +goose Up
-- +goose StatementBegin

CREATE SCHEMA IF NOT EXISTS auth_custom;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'trigger_user') THEN
        CREATE ROLE trigger_user NOLOGIN;
    END IF;
END;
$$;

GRANT USAGE ON SCHEMA auth_custom TO trigger_user;
GRANT CREATE ON SCHEMA auth_custom TO trigger_user;

DO $$
BEGIN
    EXECUTE format('GRANT TEMP ON DATABASE %I TO trigger_user', current_database());
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP ROLE IF EXISTS trigger_user;
DROP SCHEMA IF EXISTS auth_custom;

-- +goose StatementEnd
