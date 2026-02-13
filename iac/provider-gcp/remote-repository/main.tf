data "google_project" "project" {
  project_id = var.gcp_project_id
}

data "google_secret_manager_secret_version" "dockerhub_username" {
  secret  = var.dockerhub_username_secret_name
  version = "latest"
}

resource "google_secret_manager_secret_iam_member" "ar_service_agent_password_secret_access" {
  secret_id = var.dockerhub_password_secret_name
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:service-${data.google_project.project.number}@gcp-sa-artifactregistry.iam.gserviceaccount.com"
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
      for_each = trimspace(data.google_secret_manager_secret_version.dockerhub_username.secret_data) != "" ? [1] : []
      content {
        username_password_credentials {
          username                = data.google_secret_manager_secret_version.dockerhub_username.secret_data
          password_secret_version = "${var.dockerhub_password_secret_name}/versions/latest"
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
    google_secret_manager_secret_iam_member.ar_service_agent_password_secret_access,
  ]
}

resource "google_artifact_registry_repository_iam_member" "dockerhub_remote_repository_member" {
  repository = google_artifact_registry_repository.dockerhub_remote_repository.name
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${var.google_service_account_email}"
}
