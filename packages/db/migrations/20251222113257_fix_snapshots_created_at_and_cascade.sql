-- +goose Up
-- +goose StatementBegin

-- Fix 1: Set DEFAULT NOW() on created_at column so it auto-populates on insert
ALTER TABLE "public"."snapshots"
    ALTER COLUMN "created_at" SET DEFAULT NOW();

-- Fix 2: Ensure the env_id foreign key has ON DELETE CASCADE
-- First, drop the existing constraint if it exists
ALTER TABLE "public"."snapshots"
    DROP CONSTRAINT IF EXISTS "snapshots_envs_env_id";

-- Clean up orphaned snapshots: delete rows where env_id doesn't exist in envs table
-- This includes empty string env_ids and references to deleted envs
DELETE FROM "public"."snapshots"
WHERE env_id NOT IN (SELECT id FROM "public"."envs");

-- Now recreate the constraint with ON DELETE CASCADE
ALTER TABLE "public"."snapshots"
    ADD CONSTRAINT "snapshots_envs_env_id"
        FOREIGN KEY ("env_id")
            REFERENCES "public"."envs" ("id")
            ON UPDATE NO ACTION
            ON DELETE CASCADE;

COMMIT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "public"."snapshots"
    ALTER COLUMN "created_at" DROP DEFAULT;
-- Note: We don't revert the CASCADE constraint as it was likely the intended behavior
-- +goose StatementEnd

