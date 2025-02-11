terraform {
  required_providers {
    grafana = {
      source = "grafana/grafana"
    }
  }
}

variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "access_token_secret_name" {
  type        = string
  description = <<EOT
The name of the secret in GCP Secret Manager that contains the Grafana cloud access policy token.

should have permissions:
- stacks read write delete
- stack-service-accounts write
EOT

  default = "${var.prefix}grafana-api-key"
}

variable "username_secret_name" {
  type        = string
  description = <<EOT
  the stack id will be stored in this secret
EOT

  default = "${var.prefix}grafana-username"
}

// Step 1: Create a stack
provider "grafana" {
  alias                     = "cloud"
  cloud_access_policy_token = data.google_secret_manager_secret_version.grafana_cloud_access_policy_token.secret_data
}

resource "grafana_cloud_stack" "e2b_stack" {
  provider = grafana.cloud

  name        = "${var.prefix}stack"
  slug        = "${var.prefix}stack"
  region_slug = "us"
}

data "google_secret_manager_secret_version" "grafana_username" {
  secret = var.username_secret_name
}

resource "google_secret_manager_secret_version" "grafana_username" {
  secret      = data.google_secret_manager_secret_version.grafana_username.secret
  secret_data = grafana_cloud_stack.e2b_stack.id
}

data "google_secret_manager_secret_version" "grafana_api_key" {
  secret = var.access_token_secret_name
}

resource "google_secret_manager_secret_version" "grafana_api_key" {
  secret      = data.google_secret_manager_secret_version.grafana_api_key.secret
  secret_data = grafana_cloud_stack.e2b_stack.id
}
