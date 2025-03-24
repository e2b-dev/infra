BEGIN;

DROP INDEX IF EXISTS idx_envs_builds_envs;
DROP INDEX IF EXISTS idx_envs_envs_aliases;
DROP INDEX IF EXISTS idx_users_access_tokens;
DROP INDEX IF EXISTS idx_teams_envs;
DROP INDEX IF EXISTS idx_team_team_api_keys;
DROP INDEX IF EXISTS idx_teams_user_teams;
DROP INDEX IF EXISTS idx_users_user_teams;

COMMIT;