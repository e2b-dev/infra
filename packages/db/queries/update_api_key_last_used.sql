-- name: UpdateTeamApiKeyLastUsed :exec
UPDATE "public"."team_api_keys" tak SET last_used = NOW()
WHERE tak.api_key_hash = $1;
