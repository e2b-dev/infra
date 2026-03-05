#!/bin/bash
# This script configures and starts a composite health check using goss (https://github.com/goss-org/goss).
# goss validates that both Nomad and Consul are healthy, and exposes the result as an HTTP endpoint.
# AWS auto-scaling health checks can probe this endpoint to detect and replace unhealthy instances.
#
# Unlike GCP, AWS does not have fixed health check prober IP ranges, so no iptables redirect is used.
# Instead, the health check is exposed directly on its own port and can be referenced by
# ALB target group health checks or custom CloudWatch alarms.
#
# goss is a battle-tested Go binary that handles concurrency, caching, and HTTP serving natively.
# The --cache flag ensures upstream checks are not re-run on every probe, preventing slow services
# from causing health check timeouts.

set -e

readonly SCRIPT_NAME="$(basename "$0")"

function log_info {
  local readonly message="$1"
  echo "===> $SCRIPT_NAME: $message"
}

function assert_not_empty {
  local readonly arg_name="$1"
  local readonly arg_value="$2"

  if [[ -z "$arg_value" ]]; then
    log_info "ERROR: The value for '$arg_name' cannot be empty"
    exit 1
  fi
}

LISTEN_PORT="8888"
NOMAD_PORT="4646"
CONSUL_PORT="8500"
CACHE_DURATION="5s"
STARTUP_TIMEOUT=30

while [[ $# -gt 0 ]]; do
  key="$1"
  case "$key" in
    --port)
      LISTEN_PORT="$2"
      shift 2
      ;;
    --nomad-port)
      NOMAD_PORT="$2"
      shift 2
      ;;
    --consul-port)
      CONSUL_PORT="$2"
      shift 2
      ;;
    --cache)
      CACHE_DURATION="$2"
      shift 2
      ;;
    --startup-timeout)
      STARTUP_TIMEOUT="$2"
      shift 2
      ;;
    *)
      log_info "WARNING: Unrecognized argument: $key"
      shift
      ;;
  esac
done

assert_not_empty "--port" "$LISTEN_PORT"
assert_not_empty "--nomad-port" "$NOMAD_PORT"
assert_not_empty "--consul-port" "$CONSUL_PORT"

readonly GOSS_VERSION="0.4.9"
readonly GOSS_SHA256="87dd36cfa1b8b50554e6e2ca29168272e26755b19ba5438341f7c66b36decc19"
readonly GOSS_PATH="/usr/local/bin/goss"

if ! command -v goss &>/dev/null; then
  log_info "goss not found, installing v${GOSS_VERSION}..."
  curl -fsSL "https://github.com/goss-org/goss/releases/download/v${GOSS_VERSION}/goss-linux-amd64" -o "$GOSS_PATH"
  echo "${GOSS_SHA256}  ${GOSS_PATH}" | sha256sum --check
  chmod +x "$GOSS_PATH"
  log_info "goss installed: $(goss --version)"
fi

log_info "Setting up composite health check (goss) on port $LISTEN_PORT"
log_info "  Nomad endpoint: http://localhost:$NOMAD_PORT/v1/agent/health"
log_info "  Consul endpoint: http://localhost:$CONSUL_PORT/v1/status/leader"
log_info "  Cache duration: $CACHE_DURATION"

mkdir -p /opt/health-check

cat > /opt/health-check/goss.yaml << EOF
http:
  http://localhost:${NOMAD_PORT}/v1/agent/health:
    status: 200
    timeout: 2000
    allow-insecure: true
  http://localhost:${CONSUL_PORT}/v1/status/leader:
    status: 200
    timeout: 2000
    allow-insecure: true
EOF

cat > /etc/systemd/system/health-check.service << EOF
[Unit]
Description=Composite Health Check
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/goss --gossfile /opt/health-check/goss.yaml serve --listen-addr :${LISTEN_PORT} --endpoint /v1/agent/health --cache ${CACHE_DURATION} --format silent
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable health-check
systemctl start health-check

for i in $(seq 1 "$STARTUP_TIMEOUT"); do
  if ss -tlnp | grep -q ":${LISTEN_PORT}"; then
    log_info "goss is listening on port $LISTEN_PORT"
    break
  fi
  if [ "$i" -eq "$STARTUP_TIMEOUT" ]; then
    log_info "ERROR: goss did not start within ${STARTUP_TIMEOUT}s"
    exit 1
  fi
  sleep 1
done

log_info "Composite health check active on port $LISTEN_PORT"
