-- name: GetTeamWithTierByAPIKey :one
SELECT sqlc.embed(t), sqlc.embed(tier)
FROM "public"."teams" t
JOIN "public"."tiers" tier ON t.tier = tier.id
JOIN "public"."team_api_keys" tak ON t.id = tak.team_id
WHERE tak.api_key_hash = $1;
