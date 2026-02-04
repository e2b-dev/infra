#!/usr/bin/env bash
set -euo pipefail

DOMAIN=${DOMAIN:-""}
API_KEY=${API_KEY:-""}
COUNT=${COUNT:-5}
TIMEOUT_SECONDS=${TIMEOUT_SECONDS:-600}
PAUSE_TIMEOUT=${PAUSE_TIMEOUT:-10}
RESUME_WAIT=${RESUME_WAIT:-30}

CONFIG_PATH="${HOME}/.e2b/config.json"
if [ -z "${DOMAIN}" ] && [ -f "${CONFIG_PATH}" ]; then
  DOMAIN=$(jq -r '.domain // .E2B_DOMAIN // empty' "${CONFIG_PATH}")
fi

if [ -z "${API_KEY}" ] && [ -f "${CONFIG_PATH}" ]; then
  API_KEY=$(jq -r '.teamApiKey // empty' "${CONFIG_PATH}")
fi

if [ -z "${DOMAIN}" ] || [ -z "${API_KEY}" ]; then
  echo "Usage: DOMAIN=... API_KEY=... [COUNT=5] [RESUME_WAIT=30] ./scripts/smoke-resume.sh" >&2
  echo "Or ensure ${CONFIG_PATH} has domain/E2B_DOMAIN and teamApiKey." >&2
  exit 1
fi

API_URL="https://api.${DOMAIN}"

create_sandbox() {
  local auto_resume_policy="${1:-}"
  local body
  if [ -n "${auto_resume_policy}" ] && [ "${auto_resume_policy}" != "null" ] && [ "${auto_resume_policy}" != "none" ]; then
    body=$(jq -n \
      --arg policy "${auto_resume_policy}" \
      --arg template "base" \
      --argjson timeout "${TIMEOUT_SECONDS}" \
      '{autoResume:$policy, templateID:$template, timeout:$timeout, autoPause:false}')
  else
    body=$(jq -n \
      --arg template "base" \
      --argjson timeout "${TIMEOUT_SECONDS}" \
      '{templateID:$template, timeout:$timeout, autoPause:false}')
  fi

  curl -sS -X POST "${API_URL}/sandboxes" \
    -H "X-API-Key: ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "${body}"
}

pause_sandbox() {
  local id="$1"
  curl -sS -X POST "${API_URL}/sandboxes/${id}/pause" -H "X-API-Key: ${API_KEY}" >/dev/null
}

get_state() {
  local id="$1"
  curl -sS -H "X-API-Key: ${API_KEY}" "${API_URL}/sandboxes/${id}" | jq -r '.state'
}

get_metadata() {
  local id="$1"
  curl -sS -H "X-API-Key: ${API_KEY}" "${API_URL}/sandboxes/${id}" | jq -r '.metadata'
}

hit_proxy() {
  local id="$1"
  local use_token="$2"
  local url="https://49983-${id}.${DOMAIN}/"
  if [ "${use_token}" = "true" ]; then
    curl -sS -o /dev/null -w "%{http_code}" -H "X-API-Key: ${API_KEY}" "$url" >/dev/null || true
  else
    curl -sS -o /dev/null -w "%{http_code}" "$url" >/dev/null || true
  fi
}

wait_running() {
  local id="$1"
  local start="$2"
  for i in $(seq 1 "${RESUME_WAIT}"); do
    state=$(get_state "$id")
    if [ "$state" = "running" ]; then
      end=$(date +%s)
      echo "$id resumed in $((end - start))s"
      return 0
    fi
    sleep 1
  done
  echo "$id did not resume within ${RESUME_WAIT}s"
  return 1
}

run_policy_case() {
  local policy="$1"
  local use_token="$2"
  local label="$3"

  json=$(create_sandbox "$policy")
  id=$(printf "%s" "$json" | jq -r '.sandboxID')
  if [ -z "$id" ] || [ "$id" = "null" ]; then
    echo "failed to create sandbox (policy=${policy:-missing})" >&2
    echo "$json" >&2
    return 1
  fi
  echo "created $id (policy=${policy:-missing})"
  pause_sandbox "$id"
  echo "paused  $id"
  sleep "${PAUSE_TIMEOUT}"

  start=$(date +%s)
  hit_proxy "$id" "$use_token"

  if [ "$label" = "expect-resume" ]; then
    wait_running "$id" "$start"
  else
    state=$(get_state "$id")
    echo "$id state=${state} (expected paused)"
  fi
}

echo ""
echo "policy any: unauthed -> expect resume"
for i in $(seq 1 "${COUNT}"); do
  run_policy_case "any" "false" "expect-resume"
done

echo ""
echo "policy any: authed -> expect resume"
for i in $(seq 1 "${COUNT}"); do
  run_policy_case "any" "true" "expect-resume"
done

echo ""
echo "policy authed: unauthed -> expect paused"
for i in $(seq 1 "${COUNT}"); do
  run_policy_case "authed" "false" "expect-paused"
done

echo ""
echo "policy authed: authed -> expect resume"
for i in $(seq 1 "${COUNT}"); do
  run_policy_case "authed" "true" "expect-resume"
done

echo ""
echo "policy null: unauthed -> expect paused"
for i in $(seq 1 "${COUNT}"); do
  run_policy_case "null" "false" "expect-paused"
done

echo ""
echo "policy null: authed -> expect paused"
for i in $(seq 1 "${COUNT}"); do
  run_policy_case "null" "true" "expect-paused"
done
