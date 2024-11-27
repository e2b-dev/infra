-- Modify "envs" table
ALTER TABLE "public"."envs"
    ADD COLUMN "created_by" uuid NULL,
    ADD CONSTRAINT "team_api_keys_users_created_api_keys" FOREIGN KEY ("created_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
