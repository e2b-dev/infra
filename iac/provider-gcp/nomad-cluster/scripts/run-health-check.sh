#!/bin/bash
# This script configures and starts a composite health check using goss (https://github.com/goss-org/goss).
# goss validates that both Nomad and Consul are healthy, and exposes the result as an HTTP endpoint.
# GCP auto-healing health checks probe this endpoint to detect and replace unhealthy instances.
#
# To avoid changing the GCP health check definition (which would cause downtime during rollout),
# we use iptables to transparently redirect GCP health check probes from the Nomad port to goss.
# Only traffic from GCP's health check prober IP ranges is redirected; all other traffic
# (Nomad inter-node communication, CLI, etc.) reaches Nomad normally.
#
# goss is a battle-tested Go binary that handles concurrency, caching, and HTTP serving natively.
# The --cache flag ensures upstream checks are not re-run on every probe, preventing slow services
# from causing GCP health check timeouts.

set -e

readonly SCRIPT_NAME="$(basename "$0")"

# GCP health check prober IP ranges
# https://cloud.google.com/load-balancing/docs/health-check-concepts#ip-ranges
readonly GCP_HC_RANGES=("130.211.0.0/22" "35.191.0.0/16")

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

LISTEN_PORT=""
NOMAD_PORT=""
CONSUL_PORT=""
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
readonly GOSS_PATH="/usr/local/bin/goss"

if ! command -v goss &>/dev/null; then
  log_info "goss not found, installing v${GOSS_VERSION}..."
  curl -fsSL "https://github.com/goss-org/goss/releases/download/v${GOSS_VERSION}/goss-linux-amd64" -o "$GOSS_PATH"
  chmod +x "$GOSS_PATH"
  log_info "goss installed: $(goss --version)"
fi

log_info "Setting up composite health check (goss) on port $LISTEN_PORT"
log_info "  Nomad endpoint: http://localhost:$NOMAD_PORT/v1/agent/health"
log_info "  Consul endpoint: http://localhost:$CONSUL_PORT/v1/agent/self"
log_info "  Cache duration: $CACHE_DURATION"

mkdir -p /opt/health-check

cat > /opt/health-check/goss.yaml << EOF
http:
  http://localhost:${NOMAD_PORT}/v1/agent/health:
    status: 200
    timeout: 2000
    allow-insecure: true
  http://localhost:${CONSUL_PORT}/v1/agent/self:
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

# Wait for goss to be listening before redirecting health check probes.
# Until the redirect is active, GCP probes hit Nomad directly (the old behavior), so there's no risk.
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

# Redirect GCP health check probes from the Nomad port to goss.
# PREROUTING only affects external traffic; localhost traffic (goss -> Nomad) is unaffected.
for cidr in "${GCP_HC_RANGES[@]}"; do
  iptables -t nat -A PREROUTING -s "$cidr" -p tcp --dport "$NOMAD_PORT" -j REDIRECT --to-port "$LISTEN_PORT"
done

log_info "iptables redirect active: GCP health check probes on port $NOMAD_PORT -> $LISTEN_PORT"
