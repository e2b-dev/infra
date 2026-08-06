# The invited-beta API pool also hosts the workload-capacity observer. Give it
# a distinct attached identity so TAMS can authorize the observer without
# admitting the fleet-wide worker/build identity.
resource "google_service_account" "api_controller_service_account" {
  account_id   = "${var.prefix}api-controller"
  display_name = "API and Worker Capacity Controller Service Account"
}

resource "google_artifact_registry_repository_iam_member" "api_controller_orchestration_reader" {
  repository = google_artifact_registry_repository.orchestration_repository.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.api_controller_service_account.email}"

  depends_on = [time_sleep.artifact_registry_api_wait_90_seconds]
}

resource "google_artifact_registry_repository_iam_member" "api_controller_core_reader" {
  repository = google_artifact_registry_repository.core.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.api_controller_service_account.email}"

  depends_on = [time_sleep.artifact_registry_api_wait_90_seconds]
}

locals {
  api_controller_bucket_roles = {
    instance_setup = {
      bucket = google_storage_bucket.setup_bucket.name
      role   = "roles/storage.objectViewer"
    }
    controller_artifact = {
      bucket = google_storage_bucket.fc_env_pipeline_bucket.name
      role   = "roles/storage.objectViewer"
    }
    loki = {
      bucket = google_storage_bucket.loki_storage_bucket.name
      role   = "roles/storage.objectUser"
    }
  }

  api_controller_project_roles = toset([
    "roles/compute.networkViewer",
    "roles/logging.logWriter",
    "roles/monitoring.editor",
  ])
}

resource "google_storage_bucket_iam_member" "api_controller" {
  for_each = local.api_controller_bucket_roles

  bucket = each.value.bucket
  role   = each.value.role
  member = "serviceAccount:${google_service_account.api_controller_service_account.email}"
}

resource "google_project_iam_member" "api_controller" {
  for_each = local.api_controller_project_roles

  project = var.gcp_project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.api_controller_service_account.email}"
}
