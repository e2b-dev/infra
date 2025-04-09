terraform {
  required_providers {
    grafana = {
      source  = "grafana/grafana"
      version = "3.18.3"
    }
  }
}

# Create service account for Grafana
resource "google_service_account" "grafana_logging" {
  account_id   = "${var.prefix}grafana-logging"
  display_name = "Grafana Cloud Logging Service Account"
  project      = var.gcp_project_id
}

# Assign required roles to the service account
resource "google_project_iam_member" "grafana_logging_viewer" {
  project = var.gcp_project_id
  role    = "roles/logging.viewer"
  member  = "serviceAccount:${google_service_account.grafana_logging.email}"
}

resource "google_project_iam_member" "grafana_logging_accessor" {
  project = var.gcp_project_id
  role    = "roles/logging.viewAccessor"
  member  = "serviceAccount:${google_service_account.grafana_logging.email}"
}

# Create and download service account key
resource "google_service_account_key" "grafana_logging_key" {
  service_account_id = google_service_account.grafana_logging.name
}


resource "grafana_data_source" "gcloud_logs" {
  name = "gcloud-logs"
  type = "googlecloud-logging-datasource"
  json_data_encoded = jsonencode({
    authenticationType = "jwt"
    clientEmail        = google_service_account.grafana_logging.email
    defaultProject     = var.gcp_project_id
    tokenUri           = "https://oauth2.googleapis.com/token"
  })

  secure_json_data_encoded = jsonencode({
    privateKey = jsondecode(base64decode(google_service_account_key.grafana_logging_key.private_key)).private_key
  })

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

resource "grafana_data_source" "gcloud_monitoring" {
  name = "gcloud-monitoring"
  type = "stackdriver"


  json_data_encoded = jsonencode({
    authenticationType = "jwt"
    clientEmail        = google_service_account.grafana_monitoring.email
    defaultProject     = var.gcp_project_id
    tokenUri           = "https://oauth2.googleapis.com/token"
  })

  secure_json_data_encoded = jsonencode({
    privateKey = jsondecode(base64decode(google_service_account_key.grafana_monitoring_key.private_key)).private_key
  })
}

locals {
  panel_directory_path      = "${path.module}/${var.panels_directory_name}"
  dashboards_directory_path = "${path.module}/${var.dashboards_directory_name}"
  files_map = { for file in fileset(local.panel_directory_path, "**/*") :
    trimsuffix(file, ".json") => templatefile("${local.panel_directory_path}/${file}", {
      gcp_project_id  = var.gcp_project_id
      stackdriver_uid = grafana_data_source.gcloud_monitoring.uid
      prefix          = var.prefix
    })
  }
}

resource "grafana_dashboard" "dashboard" {
  for_each = { for file in fileset(local.dashboards_directory_path, "**/*") :
    trimsuffix(file, ".json") => templatefile("${local.dashboards_directory_path}/${file}", merge({
      domain_name    = var.domain_name
      gcp_project_id = var.gcp_project_id
    }, local.files_map))
  }
  config_json = each.value
}
