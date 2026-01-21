moved {
  from = google_storage_bucket.shared_persistence
  to   = google_storage_bucket.main
}

resource "google_storage_bucket" "main" {
  name     = var.bucket_name
  location = var.gcp_region

  public_access_prevention    = "enforced"
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true

  labels = var.labels
}

resource "google_storage_bucket_iam_member" "main" {
  bucket = google_storage_bucket.main.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${var.service_account_email}"
}
