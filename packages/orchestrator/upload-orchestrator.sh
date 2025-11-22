#!/bin/bash

set -euo pipefail

GCP_PROJECT_ID=${1:-}
PROVIDER=${PROVIDER:-}

chmod +x bin/orchestrator

ARTIFACT_HTTP_HOST=${ARTIFACT_HTTP_HOST:-}
ARTIFACT_HTTP_USER=${ARTIFACT_HTTP_USER:-}
ARTIFACT_HTTP_DIR=${ARTIFACT_HTTP_DIR:-}
ARTIFACT_HTTP_SSH_KEY=${ARTIFACT_HTTP_SSH_KEY:-}
ARTIFACT_HTTP_PORT=${ARTIFACT_HTTP_PORT:-22}

case "$PROVIDER" in
  gcp)
    gsutil -h "Cache-Control:no-cache, max-age=0" \
      cp bin/orchestrator "gs://${GCP_PROJECT_ID}-fc-env-pipeline/orchestrator"
    ;;
  linux)
    if [ -z "$ARTIFACT_HTTP_HOST" ] || [ -z "$ARTIFACT_HTTP_DIR" ]; then
      echo "ARTIFACT_HTTP_HOST and ARTIFACT_HTTP_DIR must be set for PROVIDER=linux" >&2
      exit 1
    fi
    if [ -n "$ARTIFACT_HTTP_SSH_KEY" ]; then
      scp -P "$ARTIFACT_HTTP_PORT" -i "$ARTIFACT_HTTP_SSH_KEY" bin/orchestrator "${ARTIFACT_HTTP_USER:+$ARTIFACT_HTTP_USER@}$ARTIFACT_HTTP_HOST:$ARTIFACT_HTTP_DIR/orchestrator"
    else
      scp -P "$ARTIFACT_HTTP_PORT" bin/orchestrator "${ARTIFACT_HTTP_USER:+$ARTIFACT_HTTP_USER@}$ARTIFACT_HTTP_HOST:$ARTIFACT_HTTP_DIR/orchestrator"
    fi
    ;;
  *)
    if [ -n "$ARTIFACT_HTTP_HOST" ] && [ -n "$ARTIFACT_HTTP_DIR" ]; then
      if [ -n "$ARTIFACT_HTTP_SSH_KEY" ]; then
        scp -P "$ARTIFACT_HTTP_PORT" -i "$ARTIFACT_HTTP_SSH_KEY" bin/orchestrator "${ARTIFACT_HTTP_USER:+$ARTIFACT_HTTP_USER@}$ARTIFACT_HTTP_HOST:$ARTIFACT_HTTP_DIR/orchestrator"
      else
        scp -P "$ARTIFACT_HTTP_PORT" bin/orchestrator "${ARTIFACT_HTTP_USER:+$ARTIFACT_HTTP_USER@}$ARTIFACT_HTTP_HOST:$ARTIFACT_HTTP_DIR/orchestrator"
      fi
    else
      gsutil -h "Cache-Control:no-cache, max-age=0" \
        cp bin/orchestrator "gs://${GCP_PROJECT_ID}-fc-env-pipeline/orchestrator"
    fi
    ;;
esac
