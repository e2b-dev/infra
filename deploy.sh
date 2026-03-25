#!/bin/bash
set -e

# Deployment script for E2B Infrastructure on GCP

# Ensure you are authenticated with gcloud
if ! gcloud auth list --filter=status:ACTIVE --format="value(account)" | grep -q "@"; then
  echo "Error: Not authenticated with gcloud. Run 'gcloud auth login' and 'gcloud auth application-default login' first."
  exit 1
fi

# Load variables (if .env file exists)
if [ -f .env ]; then
  export $(cat .env | xargs)
fi

# 1. Initialize Terraform
echo "Initializing Terraform..."
cd iac/provider-gcp
terraform init

# 2. Plan the infrastructure
echo "Planning the infrastructure..."
terraform plan -out=tfplan

# 3. Apply the infrastructure
echo "Applying the infrastructure..."
terraform apply tfplan

echo "Deployment complete."
