#!/usr/bin/env bash

set -euo pipefail

bucket=$1
build=$2

go build -o ./bin/mount-rootfs ./cmd/mount-rootfs

TEMPLATE_BUCKET_NAME=$bucket \
GOOGLE_APPLICATION_CREDENTIALS="$HOME/.config/gcloud/application_default_credentials.json" \
sudo --preserve-env=GOOGLE_APPLICATION_CREDENTIALS,TEMPLATE_BUCKET_NAME \
./bin/mount-rootfs -build $build