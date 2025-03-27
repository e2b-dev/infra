#!/bin/bash

ENV=$1

BRANCH=$(git rev-parse --abbrev-ref HEAD)
# Check if the ENV variable is set to "prod"
if [[ "$ENV" == prod* ]]; then
  # Replace prod with e2b
  ENV=$(echo $ENV | sed 's/prod/e2b/')
  if [ "$ENV" != "$BRANCH" ]; then
    echo "You are trying to deploy to $ENV from $BRANCH"
    exit 1
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
