#!/bin/bash

set -euo pipefail

count=0

for dev in /dev/nbd*; do
    if [ -f "/sys/block/${dev##*/}/pid" ]; then
        count=$((count + 1))
        echo "$dev is in use"
    fi
done

echo "Number of NBD devices in use: $count"
