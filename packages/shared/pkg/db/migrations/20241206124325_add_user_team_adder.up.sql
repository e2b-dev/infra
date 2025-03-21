BEGIN;

-- Add column to "users_teams" table
ALTER TABLE "public"."users_teams" ADD COLUMN IF NOT EXISTS "added_by" uuid NULL;
ALTER TABLE "public"."users_teams" ADD CONSTRAINT IF NOT EXISTS "users_teams_added_by_user" FOREIGN KEY ("added_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;

COMMIT; 