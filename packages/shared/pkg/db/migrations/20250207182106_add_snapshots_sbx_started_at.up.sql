BEGIN;

ALTER TABLE "public"."snapshots"
  ADD COLUMN IF NOT EXISTS "sandbox_started_at" timestamp with time zone NOT NULL 
  DEFAULT TIMESTAMP WITH TIME ZONE '1970-01-01 00:00:00 UTC';

-- Remove the default constraint after populating data
ALTER TABLE "public"."snapshots"
ALTER COLUMN "sandbox_started_at" DROP DEFAULT;

COMMIT;