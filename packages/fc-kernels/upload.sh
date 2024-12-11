#!/bin/bash

set -euo pipefail

GCP_PROJECT_ID=$1

# Check if the file exists
if [ -f "kernel_versions.txt" ]; then
  # Read kernel versions from the file
  while IFS= read -r version; do
    # Upload kernel to GCP bucket
    gsutil -h "Cache-Control:no-cache, max-age=0" cp -r "builds/vmlinux-${version}" "gs://${GCP_PROJECT_ID}-fc-kernels"

    if [ "$GCP_PROJECT_ID" == "e2b-prod" ]; then
      # Upload kernel to GCP public builds bucket
      gsutil -h "Cache-Control:no-cache, max-age=0" cp -r "builds/vmlinux-${version}" "gs://${GCP_PROJECT_ID}-public-builds/kernels/"
    fi

    rm -rf "builds/${version}"
  done <"kernel_versions.txt"

  echo "All kernels uploaded to GCP bucket successfully."
else
  echo "Error: kernel_versions.txt not found."
fi

rm -rf builds/*
