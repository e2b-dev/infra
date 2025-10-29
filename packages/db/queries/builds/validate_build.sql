-- name: ExistsWaitingTemplateBuild :one
SELECT EXISTS (
    SELECT 1
    FROM envs e
             JOIN users_teams ut ON ut.team_id = e.team_id
             JOIN access_tokens at ON at.user_id = ut.user_id
             JOIN env_builds eb ON eb.env_id = e.id
    WHERE at.access_token_hash = @access_token_hash
      AND e.id = @template_id
      AND eb.status = 'waiting'
) AS valid;