#!/bin/bash

set -euo pipefail

GCP_PROJECT_ID=$1

gsutil -h "Cache-Control:no-cache, max-age=0" cp -r "builds/*" "gs://${GCP_PROJECT_ID}-fc-versions"
if [ "$GCP_PROJECT_ID" == "e2b-prod" ]; then
  # Upload kernel to GCP public builds bucket
  gsutil -h "Cache-Control:no-cache, max-age=0" cp -r "builds/*" "gs://${GCP_PROJECT_ID}-public-builds/firecrackers/"
fi

rm -rf builds/*
