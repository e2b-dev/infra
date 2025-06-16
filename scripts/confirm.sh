#!/bin/bash

set -euo pipefail

usage() {
  echo "Usage: $0 <environment> [-y|--yes]"
  exit 1
}


if [[ $# -lt 1 ]]; then
  usage
fi

ENV="$1"
AUTO_CONFIRM_DEPLOY="${AUTO_CONFIRM_DEPLOY:-false}"


BRANCH=$(git rev-parse --abbrev-ref HEAD)
# Check if the ENV variable is set to "prod"
if [[ "$ENV" != "dev" ]]; then
  # Check if the current branch is "main"
  if [ "$BRANCH" != "main" ]; then
    echo "You are trying to deploy to $ENV from $BRANCH"
    exit 1
  fi

  if [[ "$AUTO_CONFIRM_DEPLOY" == "true" ]]; then
    echo "Auto-confirming deployment..."
    exit 0
  fi

  echo "Please type *production* to manually deploy to $ENV"
  read input
  if [ "$input" == "production" ]; then
    echo "Proceeding..."
    exit 0
  else
    echo "Invalid input. Exiting."
    exit 1
  fi
else
  exit 0
fi