-- +goose Up
-- +goose StatementBegin

-- Record the CPU info of the node a sandbox was paused on, for debugging only.
-- This is intentionally separate from env_builds.cpu_* columns, which are pinned
-- to the source build (what the guest was built with) and drive placement
-- compatibility. These columns capture where the pause physically ran.
ALTER TABLE "public"."snapshots"
    ADD COLUMN IF NOT EXISTS "origin_node_cpu_architecture" text NULL,
    ADD COLUMN IF NOT EXISTS "origin_node_cpu_family" text NULL,
    ADD COLUMN IF NOT EXISTS "origin_node_cpu_model" text NULL,
    ADD COLUMN IF NOT EXISTS "origin_node_cpu_model_name" text NULL,
    ADD COLUMN IF NOT EXISTS "origin_node_cpu_flags" text[] NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE "public"."snapshots"
    DROP COLUMN IF EXISTS "origin_node_cpu_architecture",
    DROP COLUMN IF EXISTS "origin_node_cpu_family",
    DROP COLUMN IF EXISTS "origin_node_cpu_model",
    DROP COLUMN IF EXISTS "origin_node_cpu_model_name",
    DROP COLUMN IF EXISTS "origin_node_cpu_flags";

-- +goose StatementEnd
