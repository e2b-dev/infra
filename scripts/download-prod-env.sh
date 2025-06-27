#!/bin/bash

set -euo pipefail

ENV=${1:-}
if [[ -z "$ENV" ]]; then
  echo "Usage: $0 <ENV>"
  exit 1
fi

STRIPPED_ENV="${ENV#prod-}"
ENV_FILE=".env.${ENV}"
SECRET_NAME="env_${STRIPPED_ENV}"

# Check if secret exists
if ! hcp vault-secrets secrets read "$SECRET_NAME" > /dev/null 2>&1; then
  echo "❌ Secret $SECRET_NAME does not exist."
  exit 1
fi

echo "✅ Secret $SECRET_NAME found. Fetching and decoding..."

# Get encoded value
ENCODED=$(hcp vault-secrets secrets open "$SECRET_NAME" --format=json | jq -r '.static_version.value')

# Decode to temporary file
TMP_FILE=$(mktemp)
echo "$ENCODED" | base64 -d > "$TMP_FILE"

# If file already exists, show diff and prompt
if [[ -f "$ENV_FILE" ]]; then
  if ! diff -q "$ENV_FILE" "$TMP_FILE" > /dev/null; then
    echo "⚠️ Diff detected:"

    if command -v colordiff > /dev/null; then
      colordiff -u "$ENV_FILE" "$TMP_FILE" || true
    else
      diff --no-index  "$ENV_FILE" "$TMP_FILE" || true
    fi

    read -p "Do you want to overwrite $ENV_FILE? (y/N): " CONFIRM
    if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
      echo "❌ Aborted."
      rm -f "$TMP_FILE"
      exit 1
    fi
  else
    echo "No changes detected. Keeping existing $ENV_FILE."
    rm -f "$TMP_FILE"
    exit 0
  fi
fi

# Move decoded file into place
mv "$TMP_FILE" "$ENV_FILE"
echo "✅ Update"
