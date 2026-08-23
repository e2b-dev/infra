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
      older_than = "7776000s" // 90 days in seconds
    }
  }

  depends_on = [
    google_secret_manager_secret_iam_member.ar_service_agent_password_secret_access,
  ]

  lifecycle {
    precondition {
      # google_secret_manager_secret_version.dockerhub_username/password in
      # iac/provider-gcp/init/secrets.tf are created with secret_data = " "
      # (a placeholder) and `ignore_changes = [secret_data]` — Terraform
      # deliberately never writes the real credential, an operator must set
      # it out of band. If that step is skipped, the `upstream_credentials`
      # dynamic block above silently omits itself (trimspace(" ") == "") and
      # this repository pulls Docker Hub anonymously. Every base image that
      # comes from docker.io (e.g. a project template's `FROM ubuntu:22.04`)
      # then depends on Docker Hub's unauthenticated pull/rate-limit
      # behavior for a shared GCP egress IP; Artifact Registry surfaces a
      # resulting upstream rejection to template-manager as a build-time
      # credential error with nothing pointing back at Docker Hub as the
      # actual source. Nothing else in this deployment exercises this path —
      # the operator's own runtime template pulls from lscr.io, not
      # docker.io, so a healthy operator build proves nothing about it.
      condition     = trimspace(data.google_secret_manager_secret_version.dockerhub_username.secret_data) != ""
      error_message = "remote_repository_enabled=true but the Docker Hub upstream credential secrets (${var.dockerhub_username_secret_name}, ${var.dockerhub_password_secret_name}) are still the Terraform-managed placeholder. Populate both with real Docker Hub credentials (gcloud secrets versions add ${var.dockerhub_username_secret_name} / ${var.dockerhub_password_secret_name} --project=${var.gcp_project_id}) before relying on this mirror for docker.io pulls — otherwise it silently degrades to anonymous, rate-limited Docker Hub access."
    }
  }
}

resource "google_artifact_registry_repository_iam_member" "dockerhub_remote_repository_member" {
  repository = google_artifact_registry_repository.dockerhub_remote_repository.name
  role       = "roles/artifactregistry.repoAdmin"
  member     = "serviceAccount:${var.google_service_account_email}"
}
