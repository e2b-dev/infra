BEGIN;

-- Modify "team_api_keys" table
ALTER TABLE "public"."team_api_keys"
    DROP CONSTRAINT "team_api_keys_pkey",
    ADD COLUMN "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    ADD PRIMARY KEY ("id");
-- Create index "team_api_keys_api_key_key" to table: "team_api_keys"
CREATE UNIQUE INDEX "team_api_keys_api_key_key" ON "public"."team_api_keys" ("api_key");

COMMIT; 