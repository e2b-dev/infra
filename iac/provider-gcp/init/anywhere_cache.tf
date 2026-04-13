# GCS Anywhere Cache for the template bucket.
#
# Creates SSD-backed zonal read caches in every zone of the deploy region.
# This reduces read latency and increases throughput for template fetches
# from orchestrator VMs.
#
# NOTE: The native google_storage_anywhere_cache resource requires provider
# version 7.x+. Until the provider is upgraded, we use gcloud CLI via
# terraform_data + local-exec.

data "google_compute_zones" "available" {
  count  = var.anywhere_cache_enabled ? 1 : 0
  region = var.gcp_region
  status = "UP"
}

resource "terraform_data" "template_bucket_anywhere_cache" {
  for_each = var.anywhere_cache_enabled ? toset(data.google_compute_zones.available[0].names) : toset([])

  input = {
    bucket = google_storage_bucket.fc_template_bucket.name
    zone   = each.value
  }

  provisioner "local-exec" {
    command = "gcloud storage buckets anywhere-caches create gs://${self.output.bucket} ${self.output.zone} --admission-policy=admit-on-first-miss --quiet 2>&1 || true"
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      CACHE_ID=$(gcloud storage buckets anywhere-caches list gs://${self.output.bucket} --format='value(id)' --filter="zone:${self.output.zone}" 2>/dev/null | head -1) && \
      if [ -n "$CACHE_ID" ]; then
        gcloud storage buckets anywhere-caches disable "$CACHE_ID" --quiet 2>&1 || true
      fi
    EOT
  }
}
