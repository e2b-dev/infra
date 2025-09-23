-- +goose Up
-- +goose StatementBegin
ALTER TABLE "public"."snapshots" ADD COLUMN "team_id" uuid NULL;
UPDATE "public"."snapshots" SET team_id = e.team_id FROM "public"."envs" e WHERE e.id = snapshots.env_id;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_snapshots_team_time_id
    ON public.snapshots (team_id, sandbox_started_at DESC, sandbox_id);
DROP INDEX CONCURRENTLY IF EXISTS idx_snapshots_time_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_snapshots_time_id
    ON public.snapshots (sandbox_started_at DESC, sandbox_id);
DROP INDEX CONCURRENTLY IF EXISTS idx_snapshots_team_time_id;
ALTER TABLE "public"."snapshots" DROP COLUMN "team_id";
-- +goose StatementEnd
