BEGIN;

-- Modify "envs" table
ALTER TABLE "public"."envs" ALTER COLUMN IF EXISTS "firecracker_version" SET DEFAULT 'v1.7.0-dev_8bb88311';

COMMIT; 