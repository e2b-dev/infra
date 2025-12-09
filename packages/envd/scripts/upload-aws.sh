#!/bin/bash

set -euo pipefail

BUCKET_PREFIX=$1
AWS_PROFILE=$2

chmod +x bin/envd
aws s3 cp ./bin/envd "s3://${BUCKET_PREFIX}fc-env-pipeline/envd" --profile "$AWS_PROFILE" --cache-control "no-cache, max-age=0"
