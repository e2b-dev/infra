-- name: ValidateEnvBuilds :one
SELECT at.user_id FROM envs e
JOIN users_teams ut on ut.team_id = e.team_id
JOIN access_tokens at on at.user_id = ut.user_id
WHERE at.access_token_hash = $1
  AND e.id = @template_id;