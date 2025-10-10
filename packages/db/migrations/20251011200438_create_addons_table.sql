-- +goose Up
-- +goose StatementBegin

-- Create "addons" table
CREATE TABLE IF NOT EXISTS "public"."addons"
(
    "id"                                uuid        NOT NULL DEFAULT gen_random_uuid(),
    "team_id"                           uuid        NOT NULL,
    "name"                              text        NOT NULL,
    "description"                       text        NULL,
    "extra_concurrent_sandboxes"        bigint      NOT NULL DEFAULT 0,
    "extra_concurrent_template_builds"  bigint      NOT NULL DEFAULT 0,
    "extra_max_vcpu"                    bigint      NOT NULL DEFAULT 0,
    "extra_max_ram_mb"                  bigint      NOT NULL DEFAULT 0,
    "extra_disk_mb"                     bigint      NOT NULL DEFAULT 0,
    "valid_from"                        timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "valid_to"                          timestamptz NULL ,
    "added_by"                          uuid        NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "addons_teams_addons" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "addons_users_addons" FOREIGN KEY ("added_by") REFERENCES "auth"."users" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
    CONSTRAINT "addons_valid_dates_check" CHECK (valid_to IS NULL OR valid_to > valid_from)
);

-- Enable RLS for addons table
ALTER TABLE "public"."addons" ENABLE ROW LEVEL SECURITY;

-- Create system user
INSERT INTO "auth"."users" (id, email) VALUES ('00000000-0000-0000-0000-000000000000', 'system@e2b.dev');

-- Create index on team_id for faster lookups
CREATE INDEX IF NOT EXISTS "addons_team_id_idx" ON "public"."addons" ("team_id");
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS "public"."addons" CASCADE;

-- +goose StatementEnd