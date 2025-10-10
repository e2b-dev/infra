-- name: GetTeamWithTierByTeamAndUser :one
SELECT sqlc.embed(t), sqlc.embed(tier), a.*
FROM "public"."teams" t
JOIN "public"."tiers" tier ON t.tier = tier.id
JOIN "public"."users_teams" ut ON ut.team_id = t.id
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
    ) a ON true
WHERE ut.user_id = $1 AND t.id = $2;

-- name: GetTeamsWithUsersTeamsWithTier :many
SELECT sqlc.embed(t), sqlc.embed(ut), sqlc.embed(tier), a.*
FROM "public"."teams" t
         JOIN "public"."tiers" tier ON t.tier = tier.id
         JOIN "public"."users_teams" ut ON ut.team_id = t.id
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
    ) a ON true
WHERE ut.user_id = $1;

-- name: GetTeamWithTierByAPIKeyWithUpdateLastUsed :one
UPDATE "public"."team_api_keys" tak
SET last_used = now()
FROM "public"."teams" t
         JOIN "public"."tiers" tier ON t.tier = tier.id
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
    ) a ON true
WHERE tak.team_id = t.id
  AND tak.api_key_hash = $1
RETURNING sqlc.embed(t), sqlc.embed(tier), a.*;