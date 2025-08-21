#!/bin/bash

set -euo pipefail

GCP_PROJECT_ID=$1

chmod +x bin/clean-nfs-cache

gsutil -h "Cache-Control:no-cache, max-age=0" \
  cp bin/clean-nfs-cache "gs://${GCP_PROJECT_ID}-fc-env-pipeline/clean-nfs-cache"
