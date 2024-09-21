#!/bin/bash

set -euo pipefail

GCP_PROJECT_ID=$1

gsutil -m -h "Cache-Control:no-cache, max-age=0" cp -n -r "builds/*" "gs://${GCP_PROJECT_ID}-fc-versions"

rm -rf builds/*
