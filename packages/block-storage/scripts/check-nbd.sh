#!/bin/bash

set -euo pipefail

output=""

for dev in /dev/nbd*; do
    if [ -f "/sys/block/${dev##*/}/pid" ]; then
        output="$output\n$dev"
        echo "$dev is in use"
    fi
done

count=$(echo "$output" | wc -l)

echo "Number of NBD devices in use: $((count - 1))"
