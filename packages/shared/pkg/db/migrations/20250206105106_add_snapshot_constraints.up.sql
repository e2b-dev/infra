BEGIN;

ALTER TABLE "public"."snapshots"
    ADD CONSTRAINT "snapshots_envs_env_id" FOREIGN KEY ("env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
    ADD CONSTRAINT "snapshots_envs_base_env_id" FOREIGN KEY ("base_env_id") REFERENCES "public"."envs" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;

COMMIT; 