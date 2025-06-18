resource "google_service_account" "docker_registry_service_account" {
  account_id   = "${var.prefix}docker-reverse-proxy-sa"
  display_name = "Docker Reverse Proxy Service Account"
}

resource "google_artifact_registry_repository_iam_member" "orchestration_repository_member" {
  repository = var.custom_envs_repository_name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.docker_registry_service_account.email}"
}

resource "google_service_account_key" "google_service_key" {
  service_account_id = google_service_account.docker_registry_service_account.id
}
