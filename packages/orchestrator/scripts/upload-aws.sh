#!/bin/bash

set -euo pipefail

BUCKET_PREFIX=$1
AWS_PROFILE=$2

BINARY_SOURCE=$3
BINARY_TARGET=$4

chmod +x "$BINARY_SOURCE"
aws s3 cp "$BINARY_SOURCE" "s3://${BUCKET_PREFIX}fc-env-pipeline/${BINARY_TARGET}" --cache-control "no-cache, max-age=0"
