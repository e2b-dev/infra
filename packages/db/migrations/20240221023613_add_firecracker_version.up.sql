BEGIN;

-- Modify "envs" table
ALTER TABLE "public"."envs" ADD COLUMN IF NOT EXISTS "firecracker_version" character varying NOT NULL DEFAULT 'v1.5.0_8a43b32e';

COMMIT;