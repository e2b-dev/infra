resource "google_storage_bucket" "loki_storage_bucket" {
  name     = "${var.gcp_project_id}-loki-storage"
  location = var.gcp_region

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels

  soft_delete_policy {
    retention_duration_seconds = 0
  }

  lifecycle_rule {
    condition {
      age = 8
    }

    action {
      type = "Delete"
    }
  }
}

# TODO: Not needed anymore, BUT! we may need it for rebuilding templates
resource "google_storage_bucket" "envs_docker_context" {
  name     = "${var.gcp_project_id}-envs-docker-context"
  location = var.gcp_region

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "setup_bucket" {
  location = var.gcp_region
  name     = "${var.gcp_project_id}-instance-setup"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "fc_kernels_bucket" {
  location = var.gcp_region
  name     = "${var.gcp_project_id}-fc-kernels"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "fc_versions_bucket" {
  location = var.gcp_region
  name     = "${var.gcp_project_id}-fc-versions"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "fc_env_pipeline_bucket" {
  location = var.fc_template_bucket_location
  name     = "${var.gcp_project_id}-fc-env-pipeline"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "clickhouse_bucket" {
  name     = "${var.gcp_project_id}-clickhouse"
  location = var.gcp_region

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  soft_delete_policy {
    retention_duration_seconds = 0
  }

  labels = var.labels
}

resource "google_storage_bucket" "fc_template_bucket" {
  location = var.gcp_region
  name     = var.fc_template_bucket_name

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  autoclass {
    enabled = true
  }

  soft_delete_policy {
    retention_duration_seconds = 604800
  }


  labels = var.labels
}

resource "time_sleep" "destroy_wait_5000_seconds" {
  depends_on       = [google_storage_bucket.fc_template_bucket]
  destroy_duration = "5000s"
}

resource "google_storage_anywhere_cache" "cache" {
  bucket     = google_storage_bucket.fc_template_bucket.name
  zone       = var.gcp_zone
  ttl        = "3601s"
  depends_on = [time_sleep.destroy_wait_5000_seconds]
}

resource "google_storage_bucket_iam_member" "loki_storage_iam" {
  bucket = google_storage_bucket.loki_storage_bucket.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket_iam_member" "envs_docker_context_iam" {
  bucket = google_storage_bucket.envs_docker_context.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket_iam_member" "envs_pipeline_iam" {
  bucket = google_storage_bucket.fc_env_pipeline_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket_iam_member" "instance_setup_bucket_iam" {
  bucket = google_storage_bucket.setup_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket_iam_member" "fc_kernels_bucket_iam" {
  bucket = google_storage_bucket.fc_kernels_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket_iam_member" "fc_versions_bucket_iam" {
  bucket = google_storage_bucket.fc_versions_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket_iam_member" "fc_template_bucket_iam" {
  bucket = google_storage_bucket.fc_template_bucket.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket_iam_member" "fc_template_bucket_iam_reader" {
  bucket = google_storage_bucket.fc_template_bucket.name
  role   = "roles/storage.legacyBucketReader"
  member = "serviceAccount:${var.gcp_service_account_email}"
}

resource "google_storage_bucket" "public_builds_storage_bucket" {
  count    = var.gcp_project_id == "e2b-prod" ? 1 : 0
  name     = "${var.gcp_project_id}-public-builds"
  location = var.gcp_region

  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels

  soft_delete_policy {
    retention_duration_seconds = 0
  }
}

resource "google_storage_bucket_iam_member" "public_builds_storage_bucket_iam" {
  count  = var.gcp_project_id == "e2b-prod" ? 1 : 0
  bucket = google_storage_bucket.public_builds_storage_bucket[0].name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}
