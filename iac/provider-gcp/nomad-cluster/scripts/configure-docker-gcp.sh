#!/usr/bin/env bash

set -euo pipefail

registry_host="${1:?Artifact Registry hostname is required}"
docker_home="${DOCKER_CREDENTIAL_HOME:-/root}"
nomad_config_dir="${NOMAD_DOCKER_CONFIG_DIR:-/root/docker}"

if ! command -v docker-credential-gcr >/dev/null 2>&1; then
  echo "docker-credential-gcr is not installed" >&2
  exit 1
fi

# Restrict the helper to ADC sources. On GCE this means the metadata-server
# token for the VM's attached service account; it never persists a registry
# password or a service-account private key.
HOME="$docker_home" docker-credential-gcr config --token-source="env"

config="$(jq -cn --arg registry "$registry_host" '{credHelpers: {($registry): "gcr"}}')"

# Nomad is explicitly configured to read /root/docker/config.json. Keep the
# standard Docker location in sync for operator diagnostics on the host.
install -d -m 0700 "$nomad_config_dir" "$docker_home/.docker"
umask 077
printf '%s\n' "$config" >"$nomad_config_dir/config.json"
printf '%s\n' "$config" >"$docker_home/.docker/config.json"
chmod 0600 "$nomad_config_dir/config.json" "$docker_home/.docker/config.json"
