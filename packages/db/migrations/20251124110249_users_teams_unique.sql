-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_teams_unique ON "public"."users_teams"(user_id, team_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_users_teams_unique;