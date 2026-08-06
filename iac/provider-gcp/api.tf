resource "google_artifact_registry_repository" "custom_environments_repository" {
  format        = "DOCKER"
  location      = var.gcp_region
  project       = var.gcp_project_id
  repository_id = "${var.prefix}custom-environments"
  labels        = var.labels
}

resource "google_artifact_registry_repository_iam_member" "custom_environments_repository_member" {
  project    = var.gcp_project_id
  location   = var.gcp_region
  repository = google_artifact_registry_repository.custom_environments_repository.repository_id
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${module.init.service_account_email}"
}

# The docker reverse proxy runs on the API pool and obtains short-lived ADC
# tokens from its distinct attached identity. Keep the worker/build grant above
# for template construction while granting the API identity independently.
resource "google_artifact_registry_repository_iam_member" "custom_environments_repository_api_controller_member" {
  project    = var.gcp_project_id
  location   = var.gcp_region
  repository = google_artifact_registry_repository.custom_environments_repository.repository_id
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${module.init.api_controller_service_account_email}"
}

data "google_secret_manager_secret_version" "postgres_connection_string" {
  secret = module.init.postgres_connection_string_secret_name

  depends_on = [
    google_secret_manager_secret_version.postgres_connection_string,
  ]
}

data "google_secret_manager_secret_version" "postgres_read_replica_connection_string" {
  secret = google_secret_manager_secret.postgres_read_replica_connection_string.id

  depends_on = [
    google_secret_manager_secret_version.postgres_read_replica_connection_string,
  ]
}

data "google_secret_manager_secret_version" "posthog_api_key" {
  secret = module.init.posthog_api_key_secret_name
}

data "google_secret_manager_secret_version" "api_admin_token" {
  secret = module.init.api_admin_token_secret_name
}

data "google_secret_manager_secret_version" "analytics_collector_host" {
  secret = module.init.analytics_collector_host_secret_name
}

data "google_secret_manager_secret_version" "analytics_collector_api_token" {
  secret = module.init.analytics_collector_api_token_secret_name
}

data "google_secret_manager_secret_version" "redis_cluster_url" {
  secret = module.init.redis_cluster_url_secret_version.secret
}

data "google_secret_manager_secret_version" "redis_tls_ca_base64" {
  secret = module.init.redis_tls_ca_base64_secret_version.secret
}

data "google_secret_manager_secret_version" "launch_darkly_api_key" {
  secret = module.init.launch_darkly_api_key_secret_version.secret
}

resource "google_secret_manager_secret" "postgres_read_replica_connection_string" {
  secret_id = "${var.prefix}postgres-read-replica-connection-string"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "postgres_read_replica_connection_string" {
  secret      = google_secret_manager_secret.postgres_read_replica_connection_string.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "random_password" "api_secret" {
  length  = 32
  special = false
}

resource "random_password" "clickhouse_password" {
  length  = 32
  special = false
}

resource "random_password" "clickhouse_server_secret" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "api_secret" {
  secret_id = "${var.prefix}api-secret"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "api_secret_value" {
  secret = google_secret_manager_secret.api_secret.id

  secret_data = random_password.api_secret.result
}

resource "random_password" "sandbox_access_token_hash_seed" {
  length  = 32
  special = false
}


resource "google_secret_manager_secret" "sandbox_access_token_hash_seed" {
  secret_id = "${var.prefix}sandbox-access-token-hash-seed"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "sandbox_access_token_hash_seed" {
  secret      = google_secret_manager_secret.sandbox_access_token_hash_seed.id
  secret_data = random_password.sandbox_access_token_hash_seed.result
}
