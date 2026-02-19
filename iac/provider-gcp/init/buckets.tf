resource "google_storage_bucket" "loki_storage_bucket" {
  name     = "${var.bucket_prefix}loki-storage"
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
  name     = "${var.bucket_prefix}envs-docker-context"
  location = var.gcp_region

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "setup_bucket" {
  location = var.gcp_region
  name     = "${var.bucket_prefix}instance-setup"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "fc_kernels_bucket" {
  location = var.gcp_region
  name     = "${var.bucket_prefix}fc-kernels"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "fc_versions_bucket" {
  location = var.gcp_region
  name     = "${var.bucket_prefix}fc-versions"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "fc_env_pipeline_bucket" {
  location = var.template_bucket_location
  name     = "${var.bucket_prefix}fc-env-pipeline"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket" "clickhouse_backups_bucket" {
  name     = "${var.bucket_prefix}clickhouse-backups"
  location = var.gcp_region

  lifecycle_rule {
    condition {
      age = 30
    }

    action {
      type = "Delete"
    }
  }

  public_access_prevention    = "enforced"
  storage_class               = "NEARLINE"
  uniform_bucket_level_access = true

  soft_delete_policy {
    retention_duration_seconds = 0
  }

  labels = var.labels
}

resource "google_storage_bucket" "fc_template_bucket" {
  location = var.gcp_region
  name     = (var.template_bucket_name != "" ? var.template_bucket_name : "${var.bucket_prefix}fc-templates")

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  autoclass {
    enabled                = true
    terminal_storage_class = "ARCHIVE"
  }

  lifecycle_rule {
    action {
      type = "AbortIncompleteMultipartUpload"
    }
    condition {
      age = 1 # abort multipart uploads left incomplete for 1 days
    }
  }

  soft_delete_policy {
    retention_duration_seconds = 864000 # 10 days
  }

  labels = var.labels
}

resource "google_storage_bucket" "fc_build_cache_bucket" {
  location = var.gcp_region
  name     = "${var.bucket_prefix}fc-build-cache"

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  autoclass {
    enabled = true
  }

  soft_delete_policy {
    retention_duration_seconds = 0
  }

  labels = var.labels
}

resource "google_storage_bucket_iam_member" "loki_storage_iam" {
  bucket = google_storage_bucket.loki_storage_bucket.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "envs_docker_context_iam" {
  bucket = google_storage_bucket.envs_docker_context.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "envs_pipeline_iam" {
  bucket = google_storage_bucket.fc_env_pipeline_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "instance_setup_bucket_iam" {
  bucket = google_storage_bucket.setup_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "fc_kernels_bucket_iam" {
  bucket = google_storage_bucket.fc_kernels_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "fc_versions_bucket_iam" {
  bucket = google_storage_bucket.fc_versions_bucket.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "fc_build_cache_bucket_iam" {
  bucket = google_storage_bucket.fc_build_cache_bucket.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "fc_template_bucket_iam" {
  bucket = google_storage_bucket.fc_template_bucket.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket_iam_member" "fc_template_bucket_iam_reader" {
  bucket = google_storage_bucket.fc_template_bucket.name
  role   = "roles/storage.legacyBucketReader"
  member = "serviceAccount:${google_service_account.infra_instances_service_account.email}"
}

resource "google_storage_bucket" "public_builds_storage_bucket" {
  count    = var.gcp_project_id == "e2b-prod" ? 1 : 0
  name     = "${var.bucket_prefix}public-builds"
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
