terraform {
  required_providers {
    grafana = {
      source = "grafana/grafana"
    }
  }
}

variable "prefix" {
  type    = string
  default = "e2b"
}

variable "grafana_cloud_access_policy_token_secret_name" {
  type        = string
  description = <<EOT
The name of the secret in GCP Secret Manager that contains the Grafana cloud access policy token.

should have permissions:
- stacks read write delete
- stack-service-accounts write
EOT

  default = "e2b-grafana-cloud-access-policy-token"
}

data "google_secret_manager_secret_version" "grafana_cloud_access_policy_token" {
  secret = var.grafana_cloud_access_policy_token_secret_name
}

// Step 1: Create a stack
provider "grafana" {
  alias                     = "cloud"
  cloud_access_policy_token = data.google_secret_manager_secret_version.grafana_cloud_access_policy_token.secret_data
}

resource "grafana_cloud_stack" "my_stack" {
  provider = grafana.cloud

  name        = "e2b-stack"
  slug        = "e2b-stack"
  region_slug = "us"
}

// Step 2: Create a service account and key for the stack
resource "grafana_cloud_stack_service_account" "cloud_sa" {
  provider   = grafana.cloud
  stack_slug = grafana_cloud_stack.my_stack.slug

  name        = "e2b-otel-collector-service-account"
  role        = "Admin"
  is_disabled = false
}

resource "grafana_cloud_stack_service_account_token" "cloud_sa" {
  provider   = grafana.cloud
  stack_slug = grafana_cloud_stack.my_stack.slug

  name               = "e2b-stack-service-account-token"
  service_account_id = grafana_cloud_stack_service_account.cloud_sa.id
}