# Enable Secrets Manager API
resource "google_project_service" "secrets_manager_api" {
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

resource "time_sleep" "secrets_api_wait_60_seconds" {
  depends_on = [google_project_service.secrets_manager_api]

  create_duration = "20s"
}

resource "google_service_account" "infra_instances_service_account" {
  account_id   = "${var.prefix}infra-instances"
  display_name = "Infra Instances Service Account"
}

resource "google_service_account_key" "google_service_key" {
  service_account_id = google_service_account.infra_instances_service_account.name
}

locals {
  secrets = {
    "cloudflare-api-token" = {
      generate_uuid = false
      initial_value = null
    }
    "consul-secret-id" = {
      generate_uuid = true
      initial_value = null
    }
    "nomad-secret-id" = {
      generate_uuid = true
      initial_value = null
    }
    "grafana-api-key" = {
      generate_uuid = false
      initial_value = " "
    }
    "grafana-traces-endpoint" = {
      generate_uuid = false
      initial_value = " "
    }
    "grafana-logs-endpoint" = {
      generate_uuid = false
      initial_value = " "
    }
    "grafana-metrics-endpoint" = {
      generate_uuid = false
      initial_value = " "
    }
    "grafana-traces-username" = {
      generate_uuid = false
      initial_value = " "
    }
    "grafana-logs-username" = {
      generate_uuid = false
      initial_value = " "
    }
    "grafana-metrics-username" = {
      generate_uuid = false
      initial_value = " "
    }
    "analytics-collector-host" = {
      generate_uuid = false
      initial_value = " "
    }
    "analytics-collector-api-token" = {
      generate_uuid = false
      initial_value = " "
    }
  }
}

resource "google_secret_manager_secret" "secrets" {
  for_each = local.secrets

  secret_id = "${var.prefix}${each.key}"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "random_uuid" "secret_uuids" {
  for_each = {
    for k, v in local.secrets : k => v
    if v.generate_uuid
  }
}

resource "google_secret_manager_secret_version" "secret_versions" {
  for_each = local.secrets

  secret      = google_secret_manager_secret.secrets[each.key].name
  secret_data = each.value.generate_uuid ? random_uuid.secret_uuids[each.key].result : each.value.initial_value

  dynamic "lifecycle" {
    for_each = each.value.initial_value != null ? [1] : []
    content {
      ignore_changes = [secret_data]
    }
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_artifact_registry_repository" "orchestration_repository" {
  format        = "DOCKER"
  repository_id = "e2b-orchestration"
  labels        = var.labels
}

resource "time_sleep" "artifact_registry_api_wait_60_seconds" {
  depends_on = [google_project_service.artifact_registry_api]

  create_duration = "60s"
}

resource "google_artifact_registry_repository_iam_member" "orchestration_repository_member" {
  repository = google_artifact_registry_repository.orchestration_repository.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.infra_instances_service_account.email}"

  depends_on = [time_sleep.artifact_registry_api_wait_60_seconds]
}

