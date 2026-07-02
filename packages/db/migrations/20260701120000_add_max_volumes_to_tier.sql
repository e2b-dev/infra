-- +goose Up
-- +goose StatementBegin

-- Add max_volumes column to tiers table.
-- 0 means unlimited (no quota enforced).
ALTER TABLE "public"."tiers"
    ADD COLUMN "max_volumes" bigint NOT NULL DEFAULT 0;

-- Add extra_max_volumes column to addons table.
ALTER TABLE "public"."addons"
    ADD COLUMN "extra_max_volumes" bigint NOT NULL DEFAULT 0;

-- Recreate team_limits view to include max_volumes.
CREATE OR REPLACE VIEW "team_limits"
WITH (security_invoker=on) AS
SELECT
    t.id,
    tier.max_length_hours,
    (tier.concurrent_instances + a.extra_concurrent_sandboxes) as concurrent_sandboxes,
    (tier.concurrent_template_builds + a.extra_concurrent_template_builds) as concurrent_template_builds,
    (tier.max_vcpu + a.extra_max_vcpu) as max_vcpu,
    (tier.max_ram_mb + a.extra_max_ram_mb) as max_ram_mb,
    (tier.disk_mb + a.extra_disk_mb) as disk_mb,
    (CASE WHEN tier.max_volumes = 0 THEN 0 ELSE tier.max_volumes + a.extra_max_volumes END)::bigint as max_volumes
FROM "public".teams t
JOIN "public"."tiers" tier on t.tier = tier.id
LEFT JOIN LATERAL (
    SELECT COALESCE(SUM(extra_concurrent_sandboxes),0)::bigint           as extra_concurrent_sandboxes,
           COALESCE(SUM(extra_concurrent_template_builds),0)::bigint     as extra_concurrent_template_builds,
           COALESCE(SUM(extra_max_vcpu),0)::bigint                       as extra_max_vcpu,
           COALESCE(SUM(extra_max_ram_mb),0)::bigint                     as extra_max_ram_mb,
           COALESCE(SUM(extra_disk_mb),0)::bigint                        as extra_disk_mb,
           COALESCE(SUM(extra_max_volumes),0)::bigint                    as extra_max_volumes
    FROM "public"."addons" addon
    WHERE addon.team_id = t.id
      AND addon.valid_from <= now()
      AND (addon.valid_to IS NULL OR addon.valid_to > now())
    ) a ON true;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Restore original team_limits view without max_volumes.
CREATE OR REPLACE VIEW "team_limits"
WITH (security_invoker=on) AS
SELECT
    t.id,
    tier.max_length_hours,
    (tier.concurrent_instances + a.extra_concurrent_sandboxes) as concurrent_sandboxes,
    (tier.concurrent_template_builds + a.extra_concurrent_template_builds) as concurrent_template_builds,
    (tier.max_vcpu + a.extra_max_vcpu) as max_vcpu,
    (tier.max_ram_mb + a.extra_max_ram_mb) as max_ram_mb,
    (tier.disk_mb + a.extra_disk_mb) as disk_mb
FROM "public".teams t
JOIN "public"."tiers" tier on t.tier = tier.id
LEFT JOIN LATERAL (
    SELECT COALESCE(SUM(extra_concurrent_sandboxes),0)::bigint           as extra_concurrent_sandboxes,
           COALESCE(SUM(extra_concurrent_template_builds),0)::bigint     as extra_concurrent_template_builds,
           COALESCE(SUM(extra_max_vcpu),0)::bigint                       as extra_max_vcpu,
           COALESCE(SUM(extra_max_ram_mb),0)::bigint                     as extra_max_ram_mb,
           COALESCE(SUM(extra_disk_mb),0)::bigint                        as extra_disk_mb
    FROM "public"."addons" addon
    WHERE addon.team_id = t.id
      AND addon.valid_from <= now()
      AND (addon.valid_to IS NULL OR addon.valid_to > now())
    ) a ON true;

ALTER TABLE "public"."addons" DROP COLUMN IF EXISTS "extra_max_volumes";
ALTER TABLE "public"."tiers" DROP COLUMN IF EXISTS "max_volumes";

-- +goose StatementEnd
