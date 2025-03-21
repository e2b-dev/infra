BEGIN;

-- Modify "envs" table
ALTER TABLE "public"."envs"
    ADD COLUMN IF NOT EXISTS "created_by" uuid NULL,
    ADD CONSTRAINT IF NOT EXISTS "envs_users_created_envs" FOREIGN KEY ("created_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;

COMMIT; 