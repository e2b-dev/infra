#!/bin/bash

# Set your project ID
GCP_PROJECT_ID=$1

# Get all subnets in the project
SUBNETS=$(gcloud compute networks subnets list --project="$GCP_PROJECT_ID" --format="value(name,region)")

# Loop through subnets and enable flow logs
while read -r SUBNET REGION; do
  echo "Enabling flow logs for subnet: $SUBNET in region: $REGION"
  gcloud compute networks subnets update "$SUBNET" \
    --region="$REGION" \
    --enable-flow-logs \
    --project="$GCP_PROJECT_ID" \
    --logging-flow-sampling=0.1 \
    --logging-aggregation-interval=INTERVAL_5_MIN

done <<< "$SUBNETS"

echo "VPC flow logging enabled for all subnets with a 5-minute aggregation interval."