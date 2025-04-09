terraform {
  required_providers {
    grafana = {
      source  = "grafana/grafana"
      version = "3.18.3"
    }
  }
}

data "google_secret_manager_secret_version" "grafana_api_key" {
  secret  = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-api-key"
  project = var.gcp_project_id
}

resource "grafana_cloud_stack" "e2b_stack" {
  provider = grafana

  name        = var.gcp_project_id
  slug        = replace(var.gcp_project_id, "-", "")
  region_slug = var.gcp_to_grafana_regions[var.gcp_region]
}

resource "google_secret_manager_secret_version" "grafana_username" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-username"
  secret_data = grafana_cloud_stack.e2b_stack.id
}

resource "grafana_cloud_access_policy" "otel_collector" {
  provider = grafana

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
  provider         = grafana
  region           = var.gcp_to_grafana_regions[var.gcp_region]
  access_policy_id = grafana_cloud_access_policy.otel_collector.policy_id
  name             = "otel-collector-${var.gcp_project_id}"
  display_name     = "Otel Collector for ${var.gcp_project_id}"
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
  provider     = grafana
  region       = var.gcp_to_grafana_regions[var.gcp_region]
  name         = "logs-collector-${var.gcp_project_id}"
  display_name = "Logs Collector for ${var.gcp_project_id}"

  scopes = ["logs:write"]

  realm {
    type       = "stack"
    identifier = grafana_cloud_stack.e2b_stack.id
  }
}

# Create access policy token for logs collector
resource "grafana_cloud_access_policy_token" "logs_collector" {
  provider         = grafana
  region           = var.gcp_to_grafana_regions[var.gcp_region]
  access_policy_id = grafana_cloud_access_policy.logs_collector.policy_id
  name             = "logs-collector-${var.gcp_project_id}"
  display_name     = "Logs Collector for ${var.gcp_project_id}"
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

# Enable Cloud Logging API
resource "google_project_service" "logging_api" {
  project = var.gcp_project_id
  service = "logging.googleapis.com"
}

# Enable Cloud Resource Manager API
resource "google_project_service" "resource_manager_api" {
  project = var.gcp_project_id
  service = "cloudresourcemanager.googleapis.com"
}

# configure gcp logs datasource
resource "grafana_cloud_plugin_installation" "grafana_gcp_logs_datasource" {
  provider   = grafana
  stack_slug = grafana_cloud_stack.e2b_stack.slug
  slug       = "googlecloud-logging-datasource"
  version    = "1.4.1"
}

resource "grafana_cloud_stack_service_account" "cloud_sa" {
  provider   = grafana
  stack_slug = grafana_cloud_stack.e2b_stack.slug

  name        = "cloud service account for managing datasource ${var.gcp_project_id}"
  role        = "Admin"
  is_disabled = false
}

resource "grafana_cloud_stack_service_account_token" "manage_datasource" {
  name               = "manage-datasource-${var.gcp_project_id}"
  service_account_id = grafana_cloud_stack_service_account.cloud_sa.id
  stack_slug         = grafana_cloud_stack.e2b_stack.slug
  provider           = grafana
}


resource "google_service_account" "grafana_monitoring" {
  account_id   = "${var.prefix}grafana-monitoring"
  display_name = "Grafana Cloud Monitoring Service Account"
  project      = var.gcp_project_id
}

resource "google_project_iam_member" "grafana_monitoring_viewer" {
  project = var.gcp_project_id
  role    = "roles/monitoring.viewer"
  member  = "serviceAccount:${google_service_account.grafana_monitoring.email}"
}

resource "google_service_account_key" "grafana_monitoring_key" {
  service_account_id = google_service_account.grafana_monitoring.name
}
