
# Enable Secrets Manager API
resource "google_project_service" "secrets_manager_api" {
  #project = var.gcp_project_id
  service = "secretmanager.googleapis.com"

  disable_on_destroy = false
}

# Enable Certificate Manager API
resource "google_project_service" "certificate_manager_api" {
  #project = var.gcp_project_id
  service = "certificatemanager.googleapis.com"

  disable_on_destroy = false
}

# Enable Compute Engine API
resource "google_project_service" "compute_engine_api" {
  #project = var.gcp_project_id
  service = "compute.googleapis.com"

  disable_on_destroy = false
}

# Enable Artifact Registry API
resource "google_project_service" "artifact_registry_api" {
  #project = var.gcp_project_id
  service = "artifactregistry.googleapis.com"

  disable_on_destroy = false
}

# Enable OS Config API
resource "google_project_service" "os_config_api" {
  #project = var.gcp_project_id
  service = "osconfig.googleapis.com"

  disable_on_destroy = false
}

# Enable Stackdriver Monitoring API
resource "google_project_service" "monitoring_api" {
  #project = var.gcp_project_id
  service = "monitoring.googleapis.com"

  disable_on_destroy = false
}

# Enable Stackdriver Logging API
resource "google_project_service" "logging_api" {
  #project = var.gcp_project_id
  service = "logging.googleapis.com"

  disable_on_destroy = false
}

resource "google_service_account" "infra_instances_service_account" {
  account_id   = "${var.prefix}infra-instances"
  display_name = "Infra Instances Service Account"
}

resource "google_service_account_key" "google_service_key" {
  service_account_id = google_service_account.infra_instances_service_account.name
}


resource "google_secret_manager_secret" "cloudflare_api_token" {
  secret_id = "${var.prefix}cloudflare-api-token"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret" "consul_acl_token" {
  secret_id = "${var.prefix}consul-secret-id"

  replication {
    auto {}
  }
}

resource "random_uuid" "consul_acl_token" {}

resource "google_secret_manager_secret_version" "consul_acl_token" {
  secret      = google_secret_manager_secret.consul_acl_token.name
  secret_data = random_uuid.consul_acl_token.result
}

resource "google_secret_manager_secret" "nomad_acl_token" {
  secret_id = "${var.prefix}nomad-secret-id"

  replication {
    auto {}
  }
}

resource "random_uuid" "nomad_acl_token" {}

resource "google_secret_manager_secret_version" "nomad_acl_token" {
  secret      = google_secret_manager_secret.nomad_acl_token.name
  secret_data = random_uuid.nomad_acl_token.result
}

resource "google_secret_manager_secret" "grafana_api_key" {
  secret_id = "${var.prefix}grafana-api-key"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_api_key" {
  secret      = google_secret_manager_secret.grafana_api_key.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "grafana_traces_endpoint" {
  secret_id = "${var.prefix}grafana-traces-endpoint"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_traces_endpoint" {
  secret      = google_secret_manager_secret.grafana_traces_endpoint.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "grafana_logs_endpoint" {
  secret_id = "${var.prefix}grafana-logs-endpoint"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_logs_endpoint" {
  secret      = google_secret_manager_secret.grafana_logs_endpoint.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "grafana_metrics_endpoint" {
  secret_id = "${var.prefix}grafana-metrics-endpoint"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_metrics_endpoint" {
  secret      = google_secret_manager_secret.grafana_metrics_endpoint.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "grafana_traces_username" {
  secret_id = "${var.prefix}grafana-traces-username"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_traces_username" {
  secret      = google_secret_manager_secret.grafana_traces_username.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "grafana_logs_username" {
  secret_id = "${var.prefix}grafana-logs-username"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_logs_username" {
  secret      = google_secret_manager_secret.grafana_logs_username.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "grafana_metrics_username" {
  secret_id = "${var.prefix}grafana-metrics-username"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "grafana_metrics_username" {
  secret      = google_secret_manager_secret.grafana_metrics_username.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "analytics_collector_host" {
  secret_id = "${var.prefix}analytics-collector-host"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "analytics_collector_host" {
  secret      = google_secret_manager_secret.analytics_collector_host.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "analytics_collector_api_token" {
  secret_id = "${var.prefix}analytics-collector-api-token"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "analytics_collector_api_token" {
  secret      = google_secret_manager_secret.analytics_collector_api_token.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_artifact_registry_repository" "orchestration_repository" {
  format        = "DOCKER"
  repository_id = "e2b-orchestration"
  labels        = var.labels
}

resource "google_artifact_registry_repository_iam_member" "orchestration_repository_member" {
  repository = google_artifact_registry_repository.orchestration_repository.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

