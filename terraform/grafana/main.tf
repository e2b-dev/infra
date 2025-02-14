terraform {
  required_version = ">= 1.5.0, < 1.6.0"

  backend "gcs" {
    prefix = "terraform/grafana/state"
  }
  required_providers {
    grafana = {
      source  = "grafana/grafana"
      version = "3.18.3"
    }
    google = {
      source  = "hashicorp/google"
      version = "5.31.0"
    }
  }
}

data "google_secret_manager_secret_version" "grafana_cloud_access_policy_token" {
  secret  = "${var.prefix}grafana-api-key"
  project = var.gcp_project_id
}

provider "grafana" {
  alias                     = "cloud"
  cloud_access_policy_token = data.google_secret_manager_secret_version.grafana_cloud_access_policy_token.secret_data
}

resource "grafana_cloud_stack" "e2b_stack" {
  provider = grafana.cloud

  name        = var.gcp_project_id
  slug        = replace(var.gcp_project_id, "-", "")
  region_slug = var.gcp_to_grafana_regions[var.gcp_region]
}

data "google_secret_manager_secret" "grafana_username" {
  secret_id = "${var.prefix}grafana-username"
  project   = var.gcp_project_id
}

resource "google_secret_manager_secret_version" "grafana_username" {
  secret      = data.google_secret_manager_secret.grafana_username.id
  secret_data = grafana_cloud_stack.e2b_stack.id
}


data "grafana_cloud_organization" "current" {
  id       = var.grafana_cloud_organization_id
  provider = grafana.cloud

}

resource "grafana_cloud_access_policy" "otel_collector" {
  provider = grafana.cloud

  region       = var.gcp_to_grafana_regions[var.gcp_region]
  name         = "otel-collector"
  display_name = "Otel Collector"

  scopes = ["metrics:write", "logs:write", "traces:write", "profiles:write"]

  realm {
    type       = "org"
    identifier = data.grafana_cloud_organization.current.id

    label_policy {
      selector = "{namespace=\"default\"}"
    }
  }
}

resource "grafana_cloud_access_policy_token" "otel_collector" {
  provider         = grafana.cloud
  region           = var.gcp_to_grafana_regions[var.gcp_region]
  access_policy_id = grafana_cloud_access_policy.otel_collector.policy_id
  name             = "otel-collector"
  display_name     = "Otel Collector"
}


data "google_secret_manager_secret" "otel_collector_token" {
  secret_id = "${var.prefix}grafana-otel-collector-token"
  project   = var.gcp_project_id
}

resource "google_secret_manager_secret_version" "otel_collector_token" {
  secret      = data.google_secret_manager_secret.otel_collector_token.id
  secret_data = grafana_cloud_access_policy_token.otel_collector.token
}

data "google_secret_manager_secret" "grafana_logs_url" {
  secret_id = "${var.prefix}grafana-logs-url"
  project   = var.gcp_project_id
}

resource "google_secret_manager_secret_version" "grafana_logs_url" {
  secret      = data.google_secret_manager_secret.grafana_logs_url.id
  secret_data = grafana_cloud_stack.e2b_stack.logs_url
}

# Create a new access policy for logs collector
resource "grafana_cloud_access_policy" "logs_collector" {
  provider     = grafana.cloud
  region       = var.gcp_to_grafana_regions[var.gcp_region]
  name         = "logs-collector"
  display_name = "Logs Collector"

  scopes = ["logs:write"]

  realm {
    type       = "org"
    identifier = data.grafana_cloud_organization.current.id

    label_policy {
      selector = "{namespace=\"default\"}"
    }
  }
}

# Create access policy token for logs collector
resource "grafana_cloud_access_policy_token" "logs_collector" {
  provider         = grafana.cloud
  region           = var.gcp_to_grafana_regions[var.gcp_region]
  access_policy_id = grafana_cloud_access_policy.logs_collector.policy_id
  name             = "logs-collector"
  display_name     = "Logs Collector"
}

# Get existing secret 
data "google_secret_manager_secret" "grafana_api_key_logs_collector" {
  secret_id = "${var.prefix}grafana-api-key-logs-collector"
  project   = var.gcp_project_id
}

# # Update secret with new token
resource "google_secret_manager_secret_version" "grafana_api_key_logs_collector" {
  secret      = data.google_secret_manager_secret.grafana_api_key_logs_collector.id
  secret_data = grafana_cloud_access_policy_token.logs_collector.token
}

data "google_secret_manager_secret" "grafana_logs_username" {
  secret_id = "${var.prefix}grafana-logs-username"
  project   = var.gcp_project_id
}

# Update secret with new username
resource "google_secret_manager_secret_version" "grafana_logsusername" {
  # secret = data.google_secret_manager_secret_version.grafana_username.id
  # regex to get the secret name without the version
  secret      = data.google_secret_manager_secret.grafana_logs_username.id
  secret_data = grafana_cloud_stack.e2b_stack.logs_user_id
}


