#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <upsert|delete> <gcp-project> <instance-prefix> <source> <destination>" >&2
  exit 2
fi

action="$1"
gcp_project="$2"
prefix="$3"
source="$4"
destination="$5"

case "$action" in
  upsert | delete) ;;
  *)
    echo "unsupported action: $action" >&2
    exit 2
    ;;
esac

attempts=1
if [[ "$action" == "upsert" ]]; then
  attempts=60
fi

servers=""
for _ in $(seq 1 "$attempts"); do
  servers=$(gcloud compute instances list \
    --project="$gcp_project" \
    --filter="name~^${prefix}orch-server-" \
    --format='value(name,zone)')

  [[ -n "$servers" ]] && break
  sleep 10
done

if [[ -z "$servers" ]]; then
  if [[ "$action" == "delete" ]]; then
    exit 0
  fi

  echo "No Consul server instance found for prefix ${prefix}" >&2
  exit 1
fi

read -r -d '' remote_script <<'REMOTE' || true
set -euo pipefail

config=$(mktemp)
updated=$(mktemp)
cleanup() { rm -f "$config" "$updated"; }
trap cleanup EXIT

if [[ "$ACTION" == "upsert" ]]; then
  if consul config read -kind service-intentions -name "$DESTINATION" >"$config" 2>/dev/null; then
    jq --arg source "$SOURCE" '
      del(.CreateIndex, .ModifyIndex) |
      .Sources = (((.Sources // []) | map(select(.Name != $source))) + [{"Name": $source, "Action": "allow"}])
    ' "$config" >"$updated"
  else
    jq -n --arg name "$DESTINATION" --arg source "$SOURCE" '
      {"Kind": "service-intentions", "Name": $name, "Sources": [{"Name": $source, "Action": "allow"}]}
    ' >"$updated"
  fi

  consul config write "$updated" || true
  consul config read -kind service-intentions -name "$DESTINATION" \
    | jq -e --arg source "$SOURCE" '.Sources[]? | select(.Name == $source) | select(.Action == "allow")' >/dev/null
  exit $?
fi

consul config read -kind service-intentions -name "$DESTINATION" >"$config" 2>/dev/null || exit 0
jq --arg source "$SOURCE" '
  del(.CreateIndex, .ModifyIndex) |
  .Sources = ((.Sources // []) | map(select(.Name != $source)))
' "$config" >"$updated"

if [[ "$(jq ".Sources | length" "$updated")" == "0" ]]; then
  consul config delete -kind service-intentions -name "$DESTINATION" || true
  consul config read -kind service-intentions -name "$DESTINATION" >/dev/null 2>&1 && exit 1
else
  consul config write "$updated" || true
  consul config read -kind service-intentions -name "$DESTINATION" \
    | jq -e --arg source "$SOURCE" 'if (.Sources // [] | map(select(.Name == $source)) | length) == 0 then true else empty end' >/dev/null
fi
REMOTE

printf -v quoted_script '%q' "$remote_script"
printf -v quoted_action '%q' "$action"
printf -v quoted_source '%q' "$source"
printf -v quoted_destination '%q' "$destination"

ssh_command() {
  local script="$1"

  local attempted=0
  while IFS=$'\t' read -r name zone <&3; do
    [[ -z "$name" || -z "$zone" ]] && continue
    attempted=1

    if printf '%s\n' "$script" | gcloud compute ssh "$name" \
      --zone "$zone" \
      --project="$gcp_project" \
      --command="tmp=\$(mktemp); cat > \"\$tmp\"; chmod +x \"\$tmp\"; ACTION=$quoted_action SOURCE=$quoted_source DESTINATION=$quoted_destination \"\$tmp\"; rc=\$?; rm -f \"\$tmp\"; exit \$rc"; then
      return 0
    fi
  done 3<<<"$servers"

  if [[ "$attempted" == "0" ]]; then
    echo "No Consul server instance found for prefix ${prefix}" >&2
  fi

  return 1
}

ssh_command "$remote_script"

read -r -d '' verify_script <<'REMOTE' || true
set -euo pipefail

if [[ "$ACTION" == "upsert" ]]; then
  consul config read -kind service-intentions -name "$DESTINATION" \
    | jq -e --arg source "$SOURCE" '.Sources[]? | select(.Name == $source) | select(.Action == "allow")' >/dev/null
  exit 0
fi

if ! consul config read -kind service-intentions -name "$DESTINATION" > /tmp/consul-intention-check.json 2>/dev/null; then
  exit 0
fi

jq -e --arg source "$SOURCE" 'if (.Sources // [] | map(select(.Name == $source)) | length) == 0 then true else empty end' /tmp/consul-intention-check.json >/dev/null
REMOTE

ssh_command "$verify_script"
