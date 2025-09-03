-- name: GetTeamWithTierByAPIKeyWithUpdateLastUsed :one
UPDATE "public"."team_api_keys" tak
SET last_used = now()
FROM "public"."teams" t
JOIN "public"."tiers" tier ON t.tier = tier.id
WHERE tak.team_id = t.id
  AND tak.api_key_hash = $1
RETURNING sqlc.embed(t), sqlc.embed(tier);