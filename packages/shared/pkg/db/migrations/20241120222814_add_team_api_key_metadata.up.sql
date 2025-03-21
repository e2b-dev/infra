BEGIN;

-- Modify "team_api_keys" table
ALTER TABLE "public"."team_api_keys"
    ADD COLUMN "updated_at" timestamptz NULL,
    ADD COLUMN "name" text NOT NULL DEFAULT 'Unnamed API Key',
    ADD COLUMN "last_used" timestamptz NULL,
    ADD COLUMN "created_by" uuid NULL,
    ADD CONSTRAINT "team_api_keys_users_created_api_keys" FOREIGN KEY ("created_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;

COMMIT; 