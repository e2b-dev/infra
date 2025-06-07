terraform {
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "3.0.2"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.5.1"
    }
  }
}

resource "google_artifact_registry_repository" "custom_environments_repository" {
  format        = "DOCKER"
  repository_id = "${var.prefix}custom-environments"
  labels        = var.labels
}

resource "google_artifact_registry_repository_iam_member" "custom_environments_repository_member" {
  repository = google_artifact_registry_repository.custom_environments_repository.name
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${var.google_service_account_email}"
}

data "docker_registry_image" "api_image" {
  name = "${var.gcp_region}-docker.pkg.dev/${var.gcp_project_id}/${var.orchestration_repository_name}/api:latest"
}

resource "docker_image" "api_image" {
  name          = data.docker_registry_image.api_image.name
  pull_triggers = [data.docker_registry_image.api_image.sha256_digest]
  platform      = "linux/amd64/v8"
}

resource "google_secret_manager_secret" "postgres_connection_string" {
  secret_id = "${var.prefix}postgres-connection-string"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret" "supabase_jwt_secrets" {
  secret_id = "${var.prefix}supabase-jwt-secrets"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "supabase_jwt_secrets" {
  secret      = google_secret_manager_secret.supabase_jwt_secrets.name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "redis_url" {
  secret_id = "${var.prefix}redis-url"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "redis_url" {
  secret      = google_secret_manager_secret.redis_url.name
  secret_data = "redis.service.consul"

  lifecycle {
    ignore_changes = [secret_data]
  }
}

resource "google_secret_manager_secret" "posthog_api_key" {
  secret_id = "${var.prefix}posthog-api-key"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "posthog_api_key" {
  secret      = google_secret_manager_secret.posthog_api_key.name
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