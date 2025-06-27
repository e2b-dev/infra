#!/bin/bash

set -e

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

# Fetch remote secret
echo "✅ Secret $SECRET_NAME found. Fetching and decoding..."
ENCODED=$(hcp vault-secrets secrets open "$SECRET_NAME" --format=json | jq -r '.static_version.value')

TMP_FILE=$(mktemp)
echo "$ENCODED" | base64 -d > "$TMP_FILE"

# Show diff
if ! diff -q "$TMP_FILE" "$ENV_FILE" > /dev/null; then
    echo "Diff found between local and remote .env file:"

    if command -v colordiff > /dev/null; then
      colordiff -u "$TMP_FILE" "$ENV_FILE" || true
    else
      diff --no-index "$TMP_FILE" "$ENV_FILE" || true
    fi

    read -p "Do you want to update the secret? (y/N): " CONFIRM
    if [[ "$CONFIRM" =~ ^[Yy]$ ]]; then
        base64 < "$ENV_FILE" | tr -d '\n' | hcp vault-secrets secrets create "$SECRET_NAME" --data-file=-
        echo "Secret updated."
    else
        echo "Update canceled."
    fi
else
    echo "No differences found. Nothing to update."
fi
