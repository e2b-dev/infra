#!/usr/bin/env bash
# lib/env.sh — single source of truth for all E2B service environment variables
#
# Injected into the VM as /opt/e2b/env.sh.
# Sourced by deploy-phase2.sh and start-all.sh.

# ── Common ────────────────────────────────────────────────────────────────────
export ENVIRONMENT=local
export NODE_ID="$(hostname)"
export NODE_IP="127.0.0.1"
export POSTGRES_CONNECTION_STRING="postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
export REDIS_URL="localhost:6379"
export CLICKHOUSE_CONNECTION_STRING="clickhouse://clickhouse:clickhouse@localhost:9000/default"
export OTEL_COLLECTOR_GRPC_ENDPOINT="localhost:4317"
export LOGS_COLLECTOR_ADDRESS="http://localhost:30006"
export LOKI_URL="http://localhost:3100"
export SANDBOX_ACCESS_TOKEN_HASH_SEED="--sandbox-access-token-hash-seed--"

# ── Orchestrator ──────────────────────────────────────────────────────────────
export FIRECRACKER_VERSIONS_DIR="/opt/e2b/infra/packages/fc-versions/builds"
export HOST_KERNELS_DIR="/opt/e2b/infra/packages/fc-kernels"
export HOST_ENVD_PATH="/opt/e2b/infra/packages/envd/bin/envd"
export ORCHESTRATOR_BASE_PATH="/opt/e2b/infra/packages/orchestrator/tmp/"
export ORCHESTRATOR_LOCK_PATH="/opt/e2b/infra/packages/orchestrator/tmp/.lock"
export ORCHESTRATOR_SERVICES="orchestrator,template-manager"
export ARTIFACTS_REGISTRY_PROVIDER="Local"
export STORAGE_PROVIDER="Local"
export LOCAL_TEMPLATE_STORAGE_BASE_PATH="/opt/e2b/infra/packages/orchestrator/tmp/local-template-storage"
export SANDBOX_CACHE_DIR="/opt/e2b/infra/packages/orchestrator/tmp/sandbox-cache-dir"
export SNAPSHOT_CACHE_DIR="/opt/e2b/infra/packages/orchestrator/tmp/snapshot-cache"

# ── Client-Proxy ──────────────────────────────────────────────────────────────
export EDGE_SECRET="--edge-secret--"
export EDGE_URL="http://localhost:80"
export SD_EDGE_PROVIDER="STATIC"
export SD_EDGE_STATIC="127.0.0.1"
export SD_ORCHESTRATOR_PROVIDER="STATIC"
export SD_ORCHESTRATOR_STATIC="127.0.0.1"
export SKIP_ORCHESTRATOR_READINESS_CHECK="true"
