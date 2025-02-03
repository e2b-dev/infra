-- Alter "snapshots" table
ALTER TABLE "public"."snapshots" ADD COLUMN "paused_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;
