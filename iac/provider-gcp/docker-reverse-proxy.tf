data "google_service_account" "docker_registry_service_account" {
  account_id = "${var.prefix}docker-proxy-sa"
}

resource "google_artifact_registry_repository_iam_member" "orchestration_repository_member" {
  repository = google_artifact_registry_repository.custom_environments_repository.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${data.google_service_account.docker_registry_service_account.email}"
}
