#!/usr/bin/env bash
set -euo pipefail

DOMAIN=${DOMAIN:-""}
API_KEY=${API_KEY:-""}
COUNT=${COUNT:-5}
TIMEOUT_SECONDS=${TIMEOUT_SECONDS:-600}
PAUSE_TIMEOUT=${PAUSE_TIMEOUT:-10}
RESUME_WAIT=${RESUME_WAIT:-30}

if [ -z "${DOMAIN}" ] || [ -z "${API_KEY}" ]; then
  echo "Usage: DOMAIN=... API_KEY=... [COUNT=5] [RESUME_WAIT=30] ./scripts/smoke-resume.sh" >&2
  exit 1
fi

API_URL="https://api.${DOMAIN}"

create_sandbox() {
  curl -sS -X POST "${API_URL}/sandboxes" \
    -H "X-API-Key: ${API_KEY}" \
    -H "Content-Type: application/json" \
    -d "{\"templateID\":\"base\",\"timeout\":${TIMEOUT_SECONDS},\"autoPause\":false}"
}

pause_sandbox() {
  local id="$1"
  curl -sS -X POST "${API_URL}/sandboxes/${id}/pause" -H "X-API-Key: ${API_KEY}" >/dev/null
}

get_state() {
  local id="$1"
  curl -sS -H "X-API-Key: ${API_KEY}" "${API_URL}/sandboxes/${id}" | jq -r '.state'
}

sandbox_ids=()

for i in $(seq 1 "${COUNT}"); do
  json=$(create_sandbox)
  id=$(printf "%s" "$json" | jq -r '.sandboxID')
  sandbox_ids+=("$id")
  echo "created $id"
  pause_sandbox "$id"
  echo "paused  $id"
  sleep "${PAUSE_TIMEOUT}"

done

echo "\nresume test (COUNT=${COUNT})"
for id in "${sandbox_ids[@]}"; do
  start=$(date +%s)
  url="https://49983-${id}.${DOMAIN}/"
  curl -sS -o /dev/null -w "%{http_code}" "$url" >/dev/null || true

  for i in $(seq 1 "${RESUME_WAIT}"); do
    state=$(get_state "$id")
    if [ "$state" = "running" ]; then
      end=$(date +%s)
      echo "$id resumed in $((end - start))s"
      break
    fi
    sleep 1
  done

done
