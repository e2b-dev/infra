resource "google_project_service" "cloud_build_api" {
  service = "cloudbuild.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service_identity" "cloud_build" {
  provider = google-beta

  project = var.gcp_project_id
  service = google_project_service.cloud_build_api.service
}

resource "google_service_account" "image_builder" {
  account_id   = "${var.prefix}image-builder"
  display_name = "E2B control-plane image builder"

  depends_on = [google_project_service.cloud_build_api]
}

resource "google_project_iam_member" "cloud_build_service_agent" {
  project = var.gcp_project_id
  role    = "roles/cloudbuild.serviceAgent"
  member  = "serviceAccount:${google_project_service_identity.cloud_build.email}"
}

# Cloud Build impersonates only the dedicated build identity. Runtime VMs keep
# their separate read-only Artifact Registry identity.
resource "google_service_account_iam_member" "cloud_build_image_builder_token_creator" {
  service_account_id = google_service_account.image_builder.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${google_project_service_identity.cloud_build.email}"
}

resource "google_artifact_registry_repository_iam_member" "core_image_builder" {
  repository = google_artifact_registry_repository.core.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.image_builder.email}"

  depends_on = [
    google_project_service.cloud_build_api,
    time_sleep.artifact_registry_api_wait_90_seconds,
  ]
}

resource "google_project_iam_member" "image_builder_log_writer" {
  project = var.gcp_project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.image_builder.email}"
}
