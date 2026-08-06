#!/usr/bin/env bash

set -euo pipefail

terraform_bin="${1:?terraform binary path is required}"
nomad_bin="${2:-$(command -v nomad)}"
provider_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/e2b-api-edge-ha.XXXXXX")"

test -x "${terraform_bin}"
test -x "${nomad_bin}"
command -v jq >/dev/null

cleanup() {
  rm -rf -- "${test_root}"
}
trap cleanup EXIT HUP INT TERM

for module_name in job-client-proxy job-ingress; do
  source_dir="${provider_root}/../modules/${module_name}"
  module_dir="${test_root}/${module_name}"
  mkdir -m 0700 "${module_dir}"
  cp "${source_dir}"/*.tf "${module_dir}/"
  cp "${source_dir}"/*.tftest.hcl "${module_dir}/"
  cp -R "${source_dir}/jobs" "${module_dir}/jobs"

  "${terraform_bin}" -chdir="${module_dir}" init \
    -backend=false \
    -input=false \
    >/dev/null
  "${terraform_bin}" -chdir="${module_dir}" test -no-color

  case "${module_name}" in
    job-client-proxy)
      render_expression='jsonencode(templatefile("jobs/client-proxy.hcl", {update_stanza=true, update_canary_count=0, count=2, cpu_count=1, memory_mb=1024, update_max_parallel=1, node_pool="api", proxy_port=3002, health_port=3001, image="example.invalid/client-proxy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", job_env_vars={}, entrypoints="web"}))'
      ;;
    job-ingress)
      render_expression='jsonencode(templatefile("jobs/ingress.hcl", {count=2, node_pool="api", update_stanza=true, update_canary_count=0, cpu_count=1, memory_mb=512, control_port=8900, ingress_port=8800, ingress_internal_port=8801, traefik_config="[entryPoints]", config_files={}}))'
      ;;
  esac

  printf '%s\n' "${render_expression}" \
    | "${terraform_bin}" -chdir="${module_dir}" console \
    | jq -r . \
    | jq -r . \
    | "${nomad_bin}" job validate - >/dev/null
done
