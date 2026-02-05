resource "google_artifact_registry_repository" "custom_environments_repository" {
  format        = "DOCKER"
  repository_id = "${var.prefix}custom-environments"
  labels        = var.labels
}

resource "google_artifact_registry_repository_iam_member" "custom_environments_repository_member" {
  repository = google_artifact_registry_repository.custom_environments_repository.name
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${module.init.service_account_email}"
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

resource "random_password" "api_admin_secret" {
  length  = 32
  special = true
}


resource "google_secret_manager_secret" "api_admin_token" {
  secret_id = "${var.prefix}api-admin-token"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "api_admin_token_value" {
  secret      = google_secret_manager_secret.api_admin_token.id
  secret_data = random_password.api_admin_secret.result
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