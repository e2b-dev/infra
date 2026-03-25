#!/bin/bash
set -e

# Validation script for E2B Infrastructure (GCP Native)

# Check for gcloud command
if ! command -v gcloud &> /dev/null; then
  echo "Error: gcloud command not found. Install Google Cloud CLI first."
  exit 1
fi

# 1. Check reserved static IP for NAT
echo "1. Checking reserved static IP for NAT..."
gcloud compute addresses list --format="table(name, address, region, status)" | grep -E "e2b-nat-ip|e2b-api-nat"

# 2. Check NAT Status
echo "2. Checking NAT Status..."
gcloud compute routers nats list --router=e2b-nat-router --region=$(gcloud config get-value compute/region) --format="table(name, natIpAllocateOption, sourceSubnetworkIpRangesToNat)"

# 3. Check Firewall Rules
echo "3. Checking Firewall Rules..."
gcloud compute firewall-rules list --format="table(name, network, direction, sourceRanges, targetTags, allowed, priority)" | grep -E "e2b-|orch"

# 4. Check Cloud DNS Zones
echo "4. Checking Cloud DNS Zones..."
gcloud dns managed-zones list --format="table(name, dnsName, visibility)" | grep "e2b-"

# 5. Check SSL Certificates status
echo "5. Checking GCP SSL Certificates status..."
gcloud certificate-manager certificates list --format="table(name, managed.domains, managed.status, managed.authorizationAttemptInfo)"

# 6. Test outbound connectivity (Mock check if instance is reachable)
# echo "6. Testing outbound connectivity from instance..."
# gcloud compute ssh e2b-api-orch-1 --command "curl -sI https://www.google.com"

echo "Validation complete."
