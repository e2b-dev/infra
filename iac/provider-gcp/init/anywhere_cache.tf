# GCS Anywhere Cache for the template bucket.
#
# Creates SSD-backed zonal read caches in every zone of the deploy region.
# This reduces read latency and increases throughput for template fetches
# from orchestrator VMs.

data "google_compute_zones" "available" {
  count  = var.anywhere_cache_enabled ? 1 : 0
  region = var.gcp_region
  status = "UP"
}

resource "google_storage_anywhere_cache" "template_bucket" {
  for_each = var.anywhere_cache_enabled ? toset(data.google_compute_zones.available[0].names) : toset([])

  bucket = google_storage_bucket.fc_template_bucket.name
  zone   = each.value
}
