terraform {
  required_providers {
    grafana = {
      source = "grafana/grafana"
    }
  }
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
  default = {
    "app"       = "e2b"
    "terraform" = "true"
  }
}

# wouldn't this double the number of resources or conflict with the main.tf? 
# i think there is a incompatibility with wanting grafana to be in a different module
# and having the init module contain the secrets definitions. could move the 
# secrets definitions here  here though
module "init" {
  source = "../../packages/init"

  labels = var.labels
  prefix = var.prefix
}


variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "grafana_cloud_access_policy_token_secret_name" {
  type        = string
  description = <<EOT
The name of the secret in GCP Secret Manager that contains the Grafana cloud access policy token.

should have permissions:
- stacks read write delete
- stack-service-accounts write
EOT

  default = "${var.prefix}grafana-cloud-access-policy-token"
}

variable "grafana_service_account_token_secret_name" {
  type        = string
  description = <<EOT
The name of the secret in GCP Secret Manager that contains the Grafana service account token.
EOT

  default = "${var.prefix}grafana-service-account-token"
}


data "google_secret_manager_secret_version" "grafana_cloud_access_policy_token" {
  secret = module.init.grafana_cloud_access_policy_token_secret_name
}

// Step 1: Create a stack
provider "grafana" {
  alias                     = "cloud"
  cloud_access_policy_token = data.google_secret_manager_secret_version.grafana_cloud_access_policy_token.secret_data
}

resource "grafana_cloud_stack" "my_stack" {
  provider = grafana.cloud

  name        = "${var.prefix}stack"
  slug        = "${var.prefix}stack"
  region_slug = "us"
}

// Step 2: Create a service account and key for the stack
resource "grafana_cloud_stack_service_account" "cloud_sa" {
  provider   = grafana.cloud
  stack_slug = grafana_cloud_stack.my_stack.slug

  name        = "${var.prefix}otel-collector-service-account"
  role        = "Admin"
  is_disabled = false
}

resource "grafana_cloud_stack_service_account_token" "cloud_sa" {
  provider   = grafana.cloud
  stack_slug = grafana_cloud_stack.my_stack.slug

  name               = "${var.prefix}stack-service-account-token"
  service_account_id = grafana_cloud_stack_service_account.cloud_sa.id
}

resource "google_secret_manager_secret_version" "grafana_service_account_token" {
  secret      = module.init.grafana_service_account_token_secret_name 
  secret_data = grafana_cloud_stack_service_account_token.cloud_sa.key
}
