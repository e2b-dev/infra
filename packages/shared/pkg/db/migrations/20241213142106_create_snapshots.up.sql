BEGIN;

-- Create "snapshots" table
CREATE TABLE "public"."snapshots"
(
    created_at timestamp with time zone null,
    env_id text not null,
    sandbox_id text not null,
    id uuid not null default gen_random_uuid (),
    metadata jsonb null,
    base_env_id text not null,
    constraint snapshots_pkey primary key (id)
);
ALTER TABLE "public"."snapshots" ENABLE ROW LEVEL SECURITY;

COMMIT;