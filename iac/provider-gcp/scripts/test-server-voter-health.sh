#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
health_script="${root_dir}/nomad-cluster/scripts/nomad-voter-health.py"
startup_script="${root_dir}/nomad-cluster/scripts/start-server.sh"
nomad_script="${root_dir}/nomad-cluster/scripts/run-nomad.sh"
server_tf="${root_dir}/nomad-cluster/nodepool-control-server.tf"
network_tf="${root_dir}/nomad-cluster/network/main.tf"

PYTHONDONTWRITEBYTECODE=1 python3 "${root_dir}/scripts/test_nomad_voter_health.py"
bash -n "${startup_script}" "${nomad_script}"

grep -Fq 'TOKEN_DIRECTORY = "/run/e2b-nomad-health"' "${health_script}"
grep -Fq 'NOMAD_AUTOPILOT_URL = "http://127.0.0.1:4646/v1/operator/autopilot/health"' "${health_script}"
grep -Fq '"X-Nomad-Token": token' "${health_script}"
if grep -Eq 'os\.environ|sys\.argv|print\(' "${health_script}"; then
  printf 'Nomad voter health service must not receive or print credentials.\n' >&2
  exit 1
fi

grep -Fq "health_token_dir='/run/e2b-nomad-health'" "${startup_script}"
grep -Fq 'install -d -o root -g root -m 0700 "$health_token_dir"' "${startup_script}"
grep -Fq 'chmod 0600 "$health_token_tmp"' "${startup_script}"
grep -Fq -- '--nomad-token-file "$health_token_path"' "${startup_script}"
grep -Fq 'command=/usr/bin/python3 /opt/nomad/bin/nomad-voter-health.py' "${startup_script}"
grep -Fq 'user=root' "${startup_script}"
if grep -Eq -- '--nomad-token([[:space:]"=]|$)' "${startup_script}" "${nomad_script}"; then
  printf 'Nomad credentials must not be handed off in argv.\n' >&2
  exit 1
fi
if grep -Eq '^[[:space:]]*set[[:space:]]+-x([[:space:]]|$)' "${startup_script}" "${nomad_script}"; then
  printf 'Nomad credential bootstrap must not enable shell tracing.\n' >&2
  exit 1
fi

grep -Fq 'request_path = "/healthz"' "${server_tf}"
grep -Fq 'port         = 50001' "${server_tf}"
grep -Fq 'health_check      = google_compute_health_check.server_nomad_check.id' "${server_tf}"
grep -Fq 'replacement_method = "SUBSTITUTE"' "${server_tf}"
grep -Fq 'max_unavailable_fixed = 0' "${server_tf}"
grep -Fq 'default_action_on_failure = "REPAIR"' "${server_tf}"
grep -Fq 'force_update_on_repair    = "NO"' "${server_tf}"
grep -Fq 'on_failed_health_check    = "DO_NOTHING"' "${server_tf}"

health_firewall_block="$(
  awk '
    /^resource "google_compute_firewall" "default-hc"/ { capture=1 }
    capture && seen && /^resource / { exit }
    capture { print; seen=1 }
  ' "${network_tf}"
)"
grep -Fq 'server_voter_health_port = 50001' "${network_tf}"
grep -Fq 'local.server_voter_health_port' <<<"${health_firewall_block}"
grep -Fq '"130.211.0.0/22"' <<<"${health_firewall_block}"
grep -Fq '"35.191.0.0/16"' <<<"${health_firewall_block}"
grep -Fq 'target_tags = [var.cluster_tag_name]' <<<"${health_firewall_block}"
if grep -Fq '"0.0.0.0/0"' <<<"${health_firewall_block}"; then
  printf 'Server voter health ingress must remain limited to GCP health checkers.\n' >&2
  exit 1
fi

printf 'Nomad local-voter health regression test passed.\n'
