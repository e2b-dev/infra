#!/bin/bash

# Pull a Docker image with retries. Anonymous Docker Hub pulls from shared
# runners intermittently fail with "unauthorized: authentication required"
# (Hub rate limiting); an implicit pull inside docker run then aborts the
# whole job. Near-instant when the image is already present, so it is safe
# to call both as a background warm-up and as a synchronous guarantee.

set -uo pipefail

if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <image>"
    exit 1
fi

IMAGE="$1"

for attempt in 1 2 3 4 5; do
    docker pull --quiet "$IMAGE" && exit 0
    if [ "$attempt" = 5 ]; then
        echo "::error::giving up on pulling $IMAGE"
        exit 1
    fi
    echo "pull of $IMAGE failed (attempt $attempt), retrying in $((attempt * 10))s..."
    sleep $((attempt * 10))
done
