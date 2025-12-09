#!/bin/bash

set -euo pipefail

BUCKET_PREFIX=$1
AWS_PROFILE=$2

aws s3 cp builds/ "s3://${BUCKET_PREFIX}fc-versions/" \
  --recursive \
  --cache-control "no-cache, max-age=0" \
  --profile "$AWS_PROFILE"

rm -rf builds/*
