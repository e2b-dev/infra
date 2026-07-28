#!/usr/bin/env bash
set -euo pipefail

gcloud_bin="${1:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
project_id="${2:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
zone="${3:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
image_name="${4:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
network_name="${5:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
subnet_name="${6:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
machine_type="${7:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
docker_version="${8:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
nomad_version="${9:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
consul_version="${10:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
environment="${11:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"
revision="${12:?usage: smoke-built-image.sh GCLOUD PROJECT ZONE IMAGE NETWORK SUBNET MACHINE_TYPE DOCKER_VERSION NOMAD_VERSION CONSUL_VERSION ENV REVISION}"

[[ "${project_id}" =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ ]]
[[ "${zone}" =~ ^[a-z]+-[a-z]+[0-9]-[a-z]$ ]]
[[ "${image_name}" =~ ^[a-z][a-z0-9-]{0,61}[a-z0-9]$ ]]
[[ "${network_name}" =~ ^[a-z][a-z0-9-]{0,61}[a-z0-9]$ ]]
[[ "${subnet_name}" =~ ^[a-z][a-z0-9-]{0,61}[a-z0-9]$ ]]
[[ "${machine_type}" =~ ^[a-z][a-z0-9-]{0,61}[a-z0-9]$ ]]
[[ "${docker_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "${nomad_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "${consul_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
[[ "${environment}" =~ ^[a-z][a-z0-9-]{0,61}[a-z0-9]$ ]]
[[ "${revision}" =~ ^[0-9a-f]{40}$ ]]

instance_name="monad-smoke-${revision:0:12}"
pass_marker="MONAD_IMAGE_SMOKE_PASS:${revision}"
fail_marker="MONAD_IMAGE_SMOKE_FAIL:${revision}"
max_attempts="${PACKER_SMOKE_MAX_ATTEMPTS:-60}"
poll_seconds="${PACKER_SMOKE_POLL_SECONDS:-10}"
[[ "${max_attempts}" =~ ^[1-9][0-9]*$ ]]
[[ "${poll_seconds}" =~ ^[0-9]+$ ]]

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/monad-image-smoke.XXXXXX")"
startup_script="${temp_dir}/startup.sh"
create_attempted=false
cleanup_done=false

cleanup_instance() {
  local cleanup_status=0

  if [[ "${create_attempted}" == "true" && "${cleanup_done}" != "true" ]]; then
    "${gcloud_bin}" compute instances delete "${instance_name}" \
      --project="${project_id}" \
      --zone="${zone}" \
      --delete-disks=all \
      --quiet >/dev/null 2>&1 || cleanup_status=$?
    if "${gcloud_bin}" compute instances describe "${instance_name}" \
      --project="${project_id}" \
      --zone="${zone}" \
      --format='value(name)' >/dev/null 2>&1; then
      printf 'Disposable smoke instance still exists after cleanup: %s\n' \
        "${instance_name}" >&2
      cleanup_status=1
    else
      cleanup_done=true
    fi
  fi

  return "${cleanup_status}"
}

cleanup() {
  local status=$?
  local cleanup_status=0
  cleanup_instance || cleanup_status=$?
  rm -rf -- "${temp_dir}"
  if [[ "${cleanup_status}" -ne 0 ]]; then
    exit "${cleanup_status}"
  fi
  exit "${status}"
}
trap cleanup EXIT HUP INT TERM

image_json="$(
  "${gcloud_bin}" compute images describe "${image_name}" \
    --project="${project_id}" \
    --format=json
)"
if ! jq -e \
  --arg project "${project_id}" \
  --arg image "${image_name}" \
  --arg environment "${environment}" \
  --arg revision "${revision}" '
  .name == $image
  and .status == "READY"
  and .deprecated == null
  and .id != null
  and (.selfLink | type) == "string"
  and (.selfLink | contains("/projects/" + $project + "/global/images/" + $image))
  and .labels.monad_environment == $environment
  and .labels.monad_revision == $revision
' <<<"${image_json}" >/dev/null; then
  printf 'Refusing smoke test: exact candidate image identity is not verified.\n' >&2
  exit 1
fi

if "${gcloud_bin}" compute instances describe "${instance_name}" \
  --project="${project_id}" \
  --zone="${zone}" \
  --format='value(name)' >/dev/null 2>&1; then
  printf 'Refusing to reuse pre-existing smoke instance: %s\n' \
    "${instance_name}" >&2
  exit 1
fi

cat >"${startup_script}" <<EOF
#!/usr/bin/env bash
set -Eeuo pipefail
exec > >(tee -a /var/log/monad-image-smoke.log /dev/ttyS0) 2>&1

nomad_pid=""
consul_pid=""
cleanup_guest() {
  [[ -z "\${nomad_pid}" ]] || kill "\${nomad_pid}" >/dev/null 2>&1 || true
  [[ -z "\${consul_pid}" ]] || kill "\${consul_pid}" >/dev/null 2>&1 || true
}
finish() {
  status=\$?
  cleanup_guest
  if [[ "\${status}" -ne 0 ]]; then
    printf '%s\\n' '${fail_marker}'
  fi
  exit "\${status}"
}
trap finish EXIT

systemctl is-active --quiet docker
docker info >/dev/null
[[ "\$(docker version --format '{{.Server.Version}}')" == "${docker_version}" ]]
docker compose version >/dev/null
docker buildx version >/dev/null
nomad version | head -n1 | grep -Fx 'Nomad v${nomad_version}'
consul version | head -n1 | grep -Fx 'Consul v${consul_version}'
test -c /dev/kvm
test -r /dev/kvm
test -w /dev/kvm
grep -qw vmx /proc/cpuinfo

work_dir=\$(mktemp -d /tmp/monad-agent-smoke.XXXXXX)
nomad agent -dev -bind=127.0.0.1 -data-dir="\${work_dir}/nomad" \
  >"\${work_dir}/nomad.log" 2>&1 &
nomad_pid=\$!
timeout 60 bash -c \
  'until curl -fsS http://127.0.0.1:4646/v1/agent/self >/dev/null; do sleep 1; done'
kill "\${nomad_pid}"
wait "\${nomad_pid}" 2>/dev/null || true
nomad_pid=""

consul agent -dev -bind=127.0.0.1 -client=127.0.0.1 \
  -data-dir="\${work_dir}/consul" >"\${work_dir}/consul.log" 2>&1 &
consul_pid=\$!
timeout 60 bash -c \
  'until curl -fsS http://127.0.0.1:8500/v1/status/leader | grep -q ":"; do sleep 1; done'
kill "\${consul_pid}"
wait "\${consul_pid}" 2>/dev/null || true
consul_pid=""

printf '%s\\n' '${pass_marker}'
trap - EXIT
cleanup_guest
EOF
chmod 0600 "${startup_script}"

create_attempted=true
"${gcloud_bin}" compute instances create "${instance_name}" \
  --project="${project_id}" \
  --zone="${zone}" \
  --machine-type="${machine_type}" \
  --network="${network_name}" \
  --subnet="${subnet_name}" \
  --image="${image_name}" \
  --image-project="${project_id}" \
  --no-address \
  --no-service-account \
  --no-scopes \
  --enable-nested-virtualization \
  --boot-disk-auto-delete \
  --metadata-from-file="startup-script=${startup_script}" \
  --labels="monad-purpose=image-smoke,monad-revision=${revision}" \
  --quiet >/dev/null

smoke_passed=false
for ((attempt = 1; attempt <= max_attempts; attempt++)); do
  serial_output="$(
    "${gcloud_bin}" compute instances get-serial-port-output "${instance_name}" \
      --project="${project_id}" \
      --zone="${zone}" \
      --port=1 2>/dev/null || true
  )"
  if grep -Fq "${pass_marker}" <<<"${serial_output}"; then
    smoke_passed=true
    break
  fi
  if grep -Fq "${fail_marker}" <<<"${serial_output}"; then
    break
  fi
  sleep "${poll_seconds}"
done

if [[ "${smoke_passed}" != "true" ]]; then
  printf 'Exact candidate image failed disposable boot/service smoke: %s\n' \
    "${image_name}" >&2
  exit 1
fi

cleanup_instance
trap - EXIT HUP INT TERM
rm -rf -- "${temp_dir}"
printf 'Exact candidate image passed disposable Docker, Nomad, Consul, and nested-virtualization smoke: %s\n' \
  "${image_name}"
