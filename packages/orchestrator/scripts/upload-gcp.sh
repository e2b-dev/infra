#!/bin/bash

set -euo pipefail

GCP_PROJECT_ID=$1

BINARY_SOURCE=$2
BINARY_TARGET=$3

chmod +x "$BINARY_SOURCE"
gsutil -h "Cache-Control:no-cache, max-age=0" cp "${BINARY_SOURCE}" "gs://${GCP_PROJECT_ID}-fc-env-pipeline/${BINARY_TARGET}"
