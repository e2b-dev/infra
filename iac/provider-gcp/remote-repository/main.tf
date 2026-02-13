locals {
  dockerhub_auth_enabled = var.dockerhub_auth_username != ""
}

resource "google_secret_manager_secret" "dockerhub_password" {
  count = local.dockerhub_auth_enabled ? 1 : 0

  secret_id = "${var.prefix}dockerhub-remote-repo-password"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "dockerhub_password_initial" {
  count = local.dockerhub_auth_enabled ? 1 : 0

  secret      = google_secret_manager_secret.dockerhub_password[0].name
  secret_data = " "

  lifecycle {
    ignore_changes = [secret_data]
  }
}

data "google_project" "project" {
  count = local.dockerhub_auth_enabled ? 1 : 0

  project_id = var.gcp_project_id
}

resource "google_secret_manager_secret_iam_member" "ar_service_agent_secret_access" {
  count = local.dockerhub_auth_enabled ? 1 : 0

  secret_id = google_secret_manager_secret.dockerhub_password[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:service-${data.google_project.project[0].number}@gcp-sa-artifactregistry.iam.gserviceaccount.com"
}

resource "google_artifact_registry_repository" "dockerhub_remote_repository" {
  location      = var.gcp_region
  repository_id = "${var.prefix}docker-remote-repository"
  description   = "remote docker repository"
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  remote_repository_config {
    description                 = "Docker Hub"
    disable_upstream_validation = false
    docker_repository {
      public_repository = "DOCKER_HUB"
    }

    dynamic "upstream_credentials" {
      for_each = local.dockerhub_auth_enabled ? [1] : []
      content {
        username_password_credentials {
          username                = var.dockerhub_auth_username
          password_secret_version = "${google_secret_manager_secret.dockerhub_password[0].name}/versions/latest"
        }
      }
    }
  }

  cleanup_policies {
    id     = "delete-older-than-90-days"
    action = "DELETE"
    condition {
      older_than = "90d"
    }
  }

  depends_on = [
    google_secret_manager_secret_iam_member.ar_service_agent_secret_access,
    google_secret_manager_secret_version.dockerhub_password_initial,
  ]
}

resource "google_artifact_registry_repository_iam_member" "dockerhub_remote_repository_member" {
  repository = google_artifact_registry_repository.dockerhub_remote_repository.name
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${var.google_service_account_email}"
}
