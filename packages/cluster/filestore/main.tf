resource "google_filestore_instance" "slab-cache" {
  name        = var.name
  description = "High performance slab cache"
  tier        = var.tier
  protocol    = "NFS_V4_1"

  deletion_protection_enabled = true
  deletion_protection_reason  = "If this gets removed, the orchestrator will throw tons of errors"

  file_shares {
    capacity_gb = var.capacity_gb
    name        = "slabs"
  }

  networks {
    modes = [
      "MODE_IPV4",
    ]
    network = var.network_name
  }
}
