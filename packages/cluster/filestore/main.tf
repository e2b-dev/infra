terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "6.13.0"
    }
  }
}

resource "google_filestore_instance" "slab-cache" {
  name        = "${var.cluster_name}-slab-cache"
  description = "High performance slab cache"
  tier        = "ZONAL"
  protocol    = "NFS_V4_1"

  deletion_protection_enabled = true
  deletion_protection_reason  = "If this gets removed, the orchestrator will throw tons of errors"

  file_shares {
    capacity_gb = 1024
    name        = "slab-cache"
  }

  networks {
    modes = [
      "MODE_IPV4",
    ]
    network = var.network_name
  }
}
