#!/bin/bash

set -euo pipefail

GCP_PROJECT_ID=${1:-}
PROVIDER=${PROVIDER:-}

chmod +x bin/orchestrator

ARTIFACT_SCP_HOST=${ARTIFACT_SCP_HOST:-}
ARTIFACT_SCP_USER=${ARTIFACT_SCP_USER:-}
ARTIFACT_SCP_DIR=${ARTIFACT_SCP_DIR:-}
ARTIFACT_SCP_SSH_KEY=${ARTIFACT_SCP_SSH_KEY:-}
ARTIFACT_SCP_PORT=${ARTIFACT_SCP_PORT:-22}

# Auto-load environment file if variables are missing
ENV_LAST="$(cat ../../.last_used_env 2>/dev/null || true)"
ENV_NAME="${ENV:-$ENV_LAST}"
ENV_FILE="../../.env.${ENV_NAME}"
if [ -z "$ARTIFACT_SCP_HOST" ] || [ -z "$ARTIFACT_SCP_DIR" ]; then
  if [ -f "$ENV_FILE" ]; then
    set -a
    . "$ENV_FILE"
    set +a
    ARTIFACT_SCP_HOST=${ARTIFACT_SCP_HOST:-}
    ARTIFACT_SCP_USER=${ARTIFACT_SCP_USER:-}
    ARTIFACT_SCP_DIR=${ARTIFACT_SCP_DIR:-}
    ARTIFACT_SCP_SSH_KEY=${ARTIFACT_SCP_SSH_KEY:-}
    ARTIFACT_SCP_PORT=${ARTIFACT_SCP_PORT:-22}
  fi
fi

case "$PROVIDER" in
  gcp)
    gsutil -h "Cache-Control:no-cache, max-age=0" \
      cp bin/orchestrator "gs://${GCP_PROJECT_ID}-fc-env-pipeline/orchestrator"
    ;;
  linux)
    if [ -z "$ARTIFACT_SCP_HOST" ] || [ -z "$ARTIFACT_SCP_DIR" ]; then
      echo "ARTIFACT_SCP_HOST and ARTIFACT_SCP_DIR must be set for PROVIDER=linux (set in .env.${ENV_NAME} or current shell)" >&2
      exit 1
    fi
    if [ -n "$ARTIFACT_SCP_SSH_KEY" ]; then
      scp -P "$ARTIFACT_SCP_PORT" -i "$ARTIFACT_SCP_SSH_KEY" bin/orchestrator "${ARTIFACT_SCP_USER:+$ARTIFACT_SCP_USER@}$ARTIFACT_SCP_HOST:$ARTIFACT_SCP_DIR/orchestrator"
    else
      scp -P "$ARTIFACT_SCP_PORT" bin/orchestrator "${ARTIFACT_SCP_USER:+$ARTIFACT_SCP_USER@}$ARTIFACT_SCP_HOST:$ARTIFACT_SCP_DIR/orchestrator"
    fi
    ;;
  *)
    if [ -n "$ARTIFACT_SCP_HOST" ] && [ -n "$ARTIFACT_SCP_DIR" ]; then
      if [ -n "$ARTIFACT_SCP_SSH_KEY" ]; then
        scp -P "$ARTIFACT_SCP_PORT" -i "$ARTIFACT_SCP_SSH_KEY" bin/orchestrator "${ARTIFACT_SCP_USER:+$ARTIFACT_SCP_USER@}$ARTIFACT_SCP_HOST:$ARTIFACT_SCP_DIR/orchestrator"
      else
        scp -P "$ARTIFACT_SCP_PORT" bin/orchestrator "${ARTIFACT_SCP_USER:+$ARTIFACT_SCP_USER@}$ARTIFACT_SCP_HOST:$ARTIFACT_SCP_DIR/orchestrator"
      fi
    else
      gsutil -h "Cache-Control:no-cache, max-age=0" \
        cp bin/orchestrator "gs://${GCP_PROJECT_ID}-fc-env-pipeline/orchestrator"
    fi
    ;;
esac
