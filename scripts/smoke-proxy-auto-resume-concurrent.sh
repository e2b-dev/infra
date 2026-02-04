#!/usr/bin/env bash
set -euo pipefail

DOMAIN=${DOMAIN:-""}
API_KEY=${API_KEY:-""}
COUNT=${COUNT:-5}
PORT=${PORT:-8080}
TIMEOUT_SECONDS=${TIMEOUT_SECONDS:-600}
PAUSE_WAIT=${PAUSE_WAIT:-5}
RESUME_WAIT=${RESUME_WAIT:-30}
CLEANUP=${CLEANUP:-true}
TEMPLATE_ID=${TEMPLATE_ID:-base}

CONFIG_PATH="${HOME}/.e2b/config.json"
if [ -z "${DOMAIN}" ] && [ -f "${CONFIG_PATH}" ]; then
  DOMAIN=$(jq -r '.domain // .E2B_DOMAIN // empty' "${CONFIG_PATH}")
fi

if [ -z "${API_KEY}" ] && [ -f "${CONFIG_PATH}" ]; then
  API_KEY=$(jq -r '.teamApiKey // empty' "${CONFIG_PATH}")
fi

if [ -z "${DOMAIN}" ] || [ -z "${API_KEY}" ]; then
  echo "Usage: DOMAIN=... API_KEY=... [COUNT=5] [PORT=8080] [RESUME_WAIT=30] ./scripts/smoke-proxy-auto-resume-concurrent.sh" >&2
  echo "Or ensure ${CONFIG_PATH} has domain/E2B_DOMAIN and teamApiKey." >&2
  exit 1
fi

if command -v uuidgen >/dev/null 2>&1; then
  PROBE_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
else
  PROBE_ID=$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
  )
fi

API_URL=${API_URL:-"https://api.${DOMAIN}"}

create_body=$(jq -n \
  --arg policy "authed" \
  --arg template "${TEMPLATE_ID}" \
  --arg probe "${PROBE_ID}" \
  --argjson timeout "${TIMEOUT_SECONDS}" \
  '{autoResume:$policy, metadata:{probe_id:$probe}, templateID:$template, timeout:$timeout, autoPause:false}')

create_resp=$(curl -sS -X POST "${API_URL}/sandboxes" \
  -H "X-API-Key: ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d "${create_body}")

sandbox_id=$(printf "%s" "${create_resp}" | jq -r '.sandboxID')
if [ -z "${sandbox_id}" ] || [ "${sandbox_id}" = "null" ]; then
  echo "Failed to create sandbox" >&2
  echo "${create_resp}" >&2
  exit 1
fi

traffic_token=$(printf "%s" "${create_resp}" | jq -r '.trafficAccessToken // empty')

cleanup() {
  if [ "${CLEANUP}" = "true" ]; then
    curl -sS -X DELETE "${API_URL}/sandboxes/${sandbox_id}" -H "X-API-Key: ${API_KEY}" >/dev/null || true
  fi
}
trap cleanup EXIT

echo "Sandbox: ${sandbox_id} (probe_id=${PROBE_ID})"

curl -sS -X POST "${API_URL}/sandboxes/${sandbox_id}/pause" -H "X-API-Key: ${API_KEY}" >/dev/null
state=""
for _ in $(seq 1 "${PAUSE_WAIT}"); do
  state=$(curl -sS -H "X-API-Key: ${API_KEY}" "${API_URL}/sandboxes/${sandbox_id}" | jq -r '.state')
  if [ "${state}" = "paused" ]; then
    break
  fi
  sleep 1
done
if [ "${state}" != "paused" ]; then
  echo "Sandbox did not reach paused state within ${PAUSE_WAIT}s (state=${state})" >&2
  exit 1
fi

metadata_filter=$(printf 'probe_id=%s' "${PROBE_ID}" | jq -sRr @uri)
count_before=$(curl -sS -H "X-API-Key: ${API_KEY}" "${API_URL}/sandboxes?metadata=${metadata_filter}" | jq 'length')

echo "Running sandboxes with probe_id before resume: ${count_before}"

proxy_url="https://${PORT}-${sandbox_id}.${DOMAIN}/"
headers=(-H "X-API-Key: ${API_KEY}")
if [ -n "${traffic_token}" ] && [ "${traffic_token}" != "null" ]; then
  headers+=(-H "e2b-traffic-access-token: ${traffic_token}")
fi

echo "Hitting proxy ${COUNT} times: ${proxy_url}"
for i in $(seq 1 "${COUNT}"); do
  (
    code=$(curl -sS -o /dev/null -w "%{http_code}" "${headers[@]}" "${proxy_url}" || true)
    echo "[${i}] status=${code}"
  ) &
done
wait

start=$(date +%s)
state=""
for _ in $(seq 1 "${RESUME_WAIT}"); do
  state=$(curl -sS -H "X-API-Key: ${API_KEY}" "${API_URL}/sandboxes/${sandbox_id}" | jq -r '.state')
  if [ "${state}" = "running" ]; then
    end=$(date +%s)
    echo "Sandbox running after $((end - start))s"
    break
  fi
  sleep 1
done

count_after=$(curl -sS -H "X-API-Key: ${API_KEY}" "${API_URL}/sandboxes?metadata=${metadata_filter}" | jq 'length')

echo "Running sandboxes with probe_id after resume: ${count_after}"
