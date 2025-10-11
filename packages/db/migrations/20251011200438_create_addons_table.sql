-- +goose Up
-- +goose StatementBegin

-- Create "addons" table
CREATE TABLE IF NOT EXISTS "public"."addons"
(
    "id"                          text        NOT NULL,
    "name"                        text        NOT NULL,
    "description"                 text        NULL,
    "concurrent_instances"        bigint      NOT NULL DEFAULT 0,
    "max_length_hours"            bigint      NOT NULL DEFAULT 0,
    "concurrent_template_builds"  bigint      NOT NULL DEFAULT 0,
    "max_vcpu"                    bigint      NOT NULL DEFAULT '0'::bigint,
    "max_ram_mb"                  bigint      NOT NULL DEFAULT '0'::bigint,
    "disk_mb"                     bigint      NOT NULL DEFAULT '0'::bigint,
    "team_id"                     uuid        NOT NULL,
    "valid_from"                  timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "valid_to"                    timestamp NULL ,
    "added_by"                    uuid        NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "addons_teams_addons" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    CONSTRAINT "addons_valid_dates_check" CHECK (valid_to IS NULL OR valid_to > valid_from)
);

-- Enable RLS for addons table
ALTER TABLE "public"."addons" ENABLE ROW LEVEL SECURITY;

-- Create index on team_id for faster lookups
CREATE INDEX IF NOT EXISTS "addons_team_id_idx" ON "public"."addons" ("team_id");
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS "public"."addons" CASCADE;

-- +goose StatementEnd