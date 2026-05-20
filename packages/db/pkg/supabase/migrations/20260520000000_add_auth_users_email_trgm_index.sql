-- +goose NO TRANSACTION
-- +goose Up

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX CONCURRENTLY IF NOT EXISTS auth_users_email_trgm_idx
    ON auth.users USING gin (email gin_trgm_ops);

-- +goose Down

DROP INDEX CONCURRENTLY IF EXISTS auth_users_email_trgm_idx;
