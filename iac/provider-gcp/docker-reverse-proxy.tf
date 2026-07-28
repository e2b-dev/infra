resource "google_service_account" "docker_registry_service_account" {
  account_id   = "${var.prefix}docker-reverse-proxy-sa"
  display_name = "Docker Reverse Proxy Service Account"

  depends_on = [terraform_data.runtime_credential_guard]
}

resource "google_artifact_registry_repository_iam_member" "orchestration_repository_member" {
  repository = google_artifact_registry_repository.custom_environments_repository.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.docker_registry_service_account.email}"
}
