#!/bin/bash

set -euo pipefail

for dev in /dev/nbd*; do
    if [ -f "/sys/block/${dev##*/}/pid" ]; then
        echo "$dev is in use"
        # umount --all-targets $dev
    fi
done
