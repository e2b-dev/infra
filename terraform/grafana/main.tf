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

data "google_secret_manager_secret_version" "grafana_api_key" {
  secret  = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-api-key"
  project = var.gcp_project_id
}

provider "grafana" {
  alias                     = "cloud"
  cloud_access_policy_token = data.google_secret_manager_secret_version.grafana_api_key.secret_data
}

resource "grafana_cloud_stack" "e2b_stack" {
  provider = grafana.cloud

  name        = var.gcp_project_id
  slug        = replace(var.gcp_project_id, "-", "")
  region_slug = var.gcp_to_grafana_regions[var.gcp_region]
}

resource "google_secret_manager_secret_version" "grafana_username" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-username"
  secret_data = grafana_cloud_stack.e2b_stack.id
}

resource "grafana_cloud_access_policy" "otel_collector" {
  provider = grafana.cloud

  region       = var.gcp_to_grafana_regions[var.gcp_region]
  name         = "otel-collector-${var.gcp_project_id}"
  display_name = "Otel Collector for ${var.gcp_project_id}"

  scopes = ["metrics:write", "logs:write", "traces:write", "profiles:write"]

  realm {
    type       = "stack"
    identifier = grafana_cloud_stack.e2b_stack.id
  }
}

resource "grafana_cloud_access_policy_token" "otel_collector" {
  provider         = grafana.cloud
  region           = var.gcp_to_grafana_regions[var.gcp_region]
  access_policy_id = grafana_cloud_access_policy.otel_collector.policy_id
  name             = "otel-collector"
  display_name     = "Otel Collector"
}

resource "google_secret_manager_secret_version" "otel_collector_token" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-otel-collector-token"
  secret_data = grafana_cloud_access_policy_token.otel_collector.token
}


resource "google_secret_manager_secret_version" "grafana_otlp_url" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-otlp-url"
  secret_data = grafana_cloud_stack.e2b_stack.otlp_url
}

resource "google_secret_manager_secret_version" "grafana_logs_url" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-logs-url"
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
    type       = "stack"
    identifier = grafana_cloud_stack.e2b_stack.id
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


# # Update secret with new token
resource "google_secret_manager_secret_version" "grafana_api_key_logs_collector" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-api-key-logs-collector"
  secret_data = grafana_cloud_access_policy_token.logs_collector.token
}

# Update secret with new username
resource "google_secret_manager_secret_version" "grafana_logs_user" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-logs-user"
  secret_data = grafana_cloud_stack.e2b_stack.logs_user_id
}
