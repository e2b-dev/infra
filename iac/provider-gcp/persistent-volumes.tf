resource "google_filestore_instance" "persistent-volumes" {
  for_each = var.persistent_volume_types

  name     = "${var.prefix}persistent-volume-${each.key}"
  tier     = each.value.tier
  protocol = each.value.tier == "ZONAL" ? "NFS_V4_1" : "NFS_V3"
  location = each.value.location

  deletion_protection_enabled = true
  deletion_protection_reason  = "If this gets removed, the orchestrator will throw tons of errors"

  file_shares {
    capacity_gb = each.value.capacity_gb
    name        = each.key
  }

  networks {
    modes   = ["MODE_IPV4"]
    network = "default"
  }
}
