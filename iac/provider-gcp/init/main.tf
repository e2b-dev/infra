
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

# Enable Filestore API
resource "google_project_service" "filestore_api" {
  #project = var.gcp_project_id
  service = "file.googleapis.com"

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


resource "google_secret_manager_secret" "cloudflare_api_token" {
  secret_id = "${var.prefix}cloudflare-api-token"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "consul_acl_token" {
  secret_id = "${var.prefix}consul-secret-id"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
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

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "random_uuid" "nomad_acl_token" {}

resource "google_secret_manager_secret_version" "nomad_acl_token" {
  secret      = google_secret_manager_secret.nomad_acl_token.name
  secret_data = random_uuid.nomad_acl_token.result
}



# grafana api key
resource "google_secret_manager_secret" "grafana_api_key" {
  secret_id = "${var.prefix}grafana-api-key"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "launch_darkly_api_key" {
  secret_id = "${var.prefix}launch-darkly-api-key"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "launch_darkly_api_key" {
  secret      = google_secret_manager_secret.launch_darkly_api_key.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "grafana_api_key" {
  secret      = google_secret_manager_secret.grafana_api_key.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}
resource "google_secret_manager_secret" "analytics_collector_host" {
  secret_id = "${var.prefix}analytics-collector-host"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "analytics_collector_host" {
  secret      = google_secret_manager_secret.analytics_collector_host.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "analytics_collector_api_token" {
  secret_id = "${var.prefix}analytics-collector-api-token"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "analytics_collector_api_token" {
  secret      = google_secret_manager_secret.analytics_collector_api_token.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret" "routing_domains" {
  secret_id = "${var.prefix}routing-domains"

  replication {
    auto {}
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_secret_manager_secret_version" "routing_domains" {
  secret      = google_secret_manager_secret.routing_domains.name
  secret_data = jsonencode([])

  lifecycle {
    ignore_changes = [secret_data]
  }

  depends_on = [time_sleep.secrets_api_wait_60_seconds]
}

resource "google_artifact_registry_repository" "orchestration_repository" {
  format        = "DOCKER"
  repository_id = "e2b-orchestration"
  labels        = var.labels

  depends_on = [time_sleep.artifact_registry_api_wait_90_seconds]
}

resource "time_sleep" "artifact_registry_api_wait_90_seconds" {
  depends_on = [google_project_service.artifact_registry_api]

  create_duration = "90s"
}

resource "google_artifact_registry_repository_iam_member" "orchestration_repository_member" {
  repository = google_artifact_registry_repository.orchestration_repository.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.infra_instances_service_account.email}"

  depends_on = [time_sleep.artifact_registry_api_wait_90_seconds]
}
