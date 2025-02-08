-- Alter "snapshots" table
ALTER TABLE "public"."snapshots" ADD COLUMN "sandbox_started_at" timestamp with time zone NULL;