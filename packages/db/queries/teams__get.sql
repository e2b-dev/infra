-- name: GetTeamWithTierByTeamAndUser :one
SELECT sqlc.embed(t), sqlc.embed(tier), sqlc.embed(addons)
FROM "public"."teams" t
JOIN "public"."tiers" tier ON t.tier = tier.id
JOIN "public"."users_teams" ut ON ut.team_id = t.id
LEFT JOIN LATERAL (
    SELECT SUM(concurrent_sandboxes)       as concurrent_sandboxes,
           SUM(concurrent_template_builds) as concurrent_template_builds,
           SUM(vcpu)                       as vcpu,
           SUM(ram_mb)                     as ram_mb,
           coalesce(MAX(disk_mb),0)        as disk_mb,
           SUM(max_vcpu)                   as max_vcpu
    FROM "public"."addons" addon
    WHERE addon.team_id = t.id
      AND addon.valid_from <= now()
      AND (addon.valid_to IS NULL OR addon.valid_to > now())
    ) addons ON true
WHERE ut.user_id = $1 AND t.id = $2;

-- name: GetTeamsWithUsersTeamsWithTier :many
SELECT sqlc.embed(t), sqlc.embed(ut), sqlc.embed(tier), sqlc.embed(addons)
FROM "public"."teams" t
         JOIN "public"."tiers" tier ON t.tier = tier.id
         JOIN "public"."users_teams" ut ON ut.team_id = t.id
         LEFT JOIN LATERAL (
    SELECT SUM(concurrent_sandboxes)       as concurrent_sandboxes,
           SUM(concurrent_template_builds) as concurrent_template_builds,
           SUM(vcpu)                       as vcpu,
           SUM(ram_mb)                     as ram_mb,
           coalesce(MAX(disk_mb),0)        as disk_mb,
           SUM(max_vcpu)                   as max_vcpu
    FROM "public"."addons" addon
    WHERE addon.team_id = t.id
      AND addon.valid_from <= now()
      AND (addon.valid_to IS NULL OR addon.valid_to > now())
    ) addons ON true
WHERE ut.user_id = $1;

-- name: GetTeamWithTierByAPIKeyWithUpdateLastUsed :one
UPDATE "public"."team_api_keys" tak
SET last_used = now()
FROM "public"."teams" t
         JOIN "public"."tiers" tier ON t.tier = tier.id
         LEFT JOIN LATERAL (
    SELECT SUM(concurrent_sandboxes)       as concurrent_sandboxes,
           SUM(concurrent_template_builds) as concurrent_template_builds,
           SUM(vcpu)                       as vcpu,
           SUM(ram_mb)                     as ram_mb,
           coalesce(MAX(disk_mb),0)        as disk_mb,
           SUM(max_vcpu)                   as max_vcpu
    FROM "public"."addons" addon
    WHERE addon.team_id = t.id
      AND addon.valid_from <= now()
      AND (addon.valid_to IS NULL OR addon.valid_to > now())
    ) addons ON true
WHERE tak.team_id = t.id
  AND tak.api_key_hash = $1
RETURNING sqlc.embed(t), sqlc.embed(tier), sqlc.embed(addons);