resource "google_artifact_registry_repository" "docker_remote_repository" {
  location      = var.gcp_region
  repository_id = "${var.prefix}docker-remote-repository"
  description   = "remote docker repository"
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  remote_repository_config {
    description = "Docker Hub"
    docker_repository {
      public_repository = "DOCKER_HUB"
    }
  }

  cleanup_policies {
    id     = "delete-older-than-90-days"
    action = "DELETE"
    condition {
      older_than = "90d"
    }
  }
}

resource "google_artifact_registry_repository_iam_member" "docker_remote_repository_member" {
  repository = google_artifact_registry_repository.docker_remote_repository.name
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${var.google_service_account_email}"
}
