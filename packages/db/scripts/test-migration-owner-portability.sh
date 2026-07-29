#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
migration="${script_dir}/../migrations/20231220094836_create_triggers_and_policies.sql"
container_name="e2b-migration-owner-$$"

cleanup() {
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run \
  --detach \
  --rm \
  --name "${container_name}" \
  --env POSTGRES_PASSWORD=admin-test \
  --volume "${migration}:/migration.sql:ro" \
  postgres:16-alpine >/dev/null

for _ in {1..30}; do
  if docker exec "${container_name}" pg_isready --username postgres >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "${container_name}" pg_isready --username postgres >/dev/null

docker exec "${container_name}" \
  psql --username postgres --set ON_ERROR_STOP=1 \
  --command "CREATE ROLE e2b LOGIN PASSWORD 'e2b-test' CREATEROLE;" >/dev/null
docker exec "${container_name}" createdb --username postgres --owner e2b e2b

docker exec \
  --env PGPASSWORD=e2b-test \
  --interactive \
  "${container_name}" \
  psql --username e2b --dbname e2b --set ON_ERROR_STOP=1 >/dev/null <<'SQL'
CREATE SCHEMA auth;
CREATE SCHEMA extensions;
CREATE TABLE public.tiers (
  id text PRIMARY KEY,
  name text NOT NULL,
  vcpu bigint NOT NULL,
  ram_mb bigint NOT NULL,
  disk_mb bigint NOT NULL,
  concurrent_instances bigint NOT NULL
);
CREATE TABLE public.teams (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  is_default boolean NOT NULL,
  tier text NOT NULL,
  email text
);
CREATE TABLE public.users_teams (user_id uuid, team_id uuid);
CREATE TABLE public.team_api_keys (team_id uuid, api_key text);
CREATE TABLE public.access_tokens (user_id uuid, access_token text);
CREATE TABLE auth.users (id uuid, email text);
SQL

docker exec \
  --env PGPASSWORD=e2b-test \
  "${container_name}" \
  psql \
    --username e2b \
    --dbname e2b \
    --set ON_ERROR_STOP=1 \
    --file /migration.sql >/dev/null

result="$(
  docker exec \
    --env PGPASSWORD=e2b-test \
    "${container_name}" \
    psql \
      --username e2b \
      --dbname e2b \
      --tuples-only \
      --no-align \
      --set ON_ERROR_STOP=1 \
      --command "
        SELECT
          current_user,
          pg_has_role(current_user, 'trigger_user', 'MEMBER'),
          COUNT(*) FILTER (
            WHERE pg_get_userbyid(proowner) = 'trigger_user'
          )
        FROM pg_proc
        WHERE proname IN (
          'generate_default_team_trigger',
          'generate_teams_api_keys_trigger',
          'generate_access_token_trigger'
        )
        GROUP BY current_user;
      "
)"

if [[ "${result}" != "e2b|t|3" ]]; then
  printf 'unexpected migration ownership result: %s\n' "${result}" >&2
  exit 1
fi
