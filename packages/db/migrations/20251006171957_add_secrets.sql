-- +goose Up
-- +goose StatementBegin

-- Create "secrets" table
CREATE TABLE IF NOT EXISTS "public"."secrets"
(
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "created_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" timestamptz NULL,
    "team_id" uuid NOT NULL,
    "label" text NOT NULL DEFAULT 'Unlabelled Secret',
    "description" text NOT NULL DEFAULT '',
    "allowlist" text[] NOT NULL,
    PRIMARY KEY ("id"),
    CONSTRAINT "secrets_teams_team_secrets" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);

-- Create index for efficient team queries
CREATE INDEX IF NOT EXISTS idx_teams_secrets ON public.secrets (team_id);

-- Enable RLS
ALTER TABLE "public"."secrets" ENABLE ROW LEVEL SECURITY;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Drop table
DROP TABLE IF EXISTS "public"."secrets";

-- +goose StatementEnd