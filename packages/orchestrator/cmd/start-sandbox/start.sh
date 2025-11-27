#!/usr/bin/env bash

set -euo pipefail

go build -o ./bin/start-sandbox ./cmd/start-sandbox

GOOGLE_APPLICATION_CREDENTIALS="$HOME/.config/gcloud/application_default_credentials.json" \
sudo --preserve-env=GOOGLE_APPLICATION_CREDENTIALS,TEMPLATE_BUCKET_NAME \
./bin/start-sandbox "$@"
