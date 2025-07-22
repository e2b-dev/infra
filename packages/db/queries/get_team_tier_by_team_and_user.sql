-- name: GetTeamWithTierByTeamAndUser :one
SELECT sqlc.embed(t), sqlc.embed(tier)
FROM "public"."teams" t
JOIN "public"."tiers" tier ON t.tier = tier.id
JOIN "public"."users_teams" ut ON ut.team_id = t.id
WHERE ut.user_id = $1 AND t.id = $2;
