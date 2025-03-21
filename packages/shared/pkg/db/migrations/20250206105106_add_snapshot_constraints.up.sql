BEGIN;

ALTER TABLE "public"."snapshots"
    ADD CONSTRAINT IF NOT EXISTS "snapshots_envs_env_id" FOREIGN KEY ("env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    ADD CONSTRAINT IF NOT EXISTS "snapshots_envs_base_env_id" FOREIGN KEY ("base_env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;

COMMIT; 