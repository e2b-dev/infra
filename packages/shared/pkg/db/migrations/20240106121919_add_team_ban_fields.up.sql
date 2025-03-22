BEGIN;

-- Modify "teams" table
ALTER TABLE "public"."teams"
    ADD COLUMN IF NOT EXISTS "is_banned" boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS "blocked_reason" text NULL;

COMMIT; 