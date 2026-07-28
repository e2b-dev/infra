#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 3 || "$1" != "show" || "$2" != "-json" ]]; then
  printf 'fake terraform supports only: show -json PLAN_PATH\n' >&2
  exit 2
fi

cat "$3"
