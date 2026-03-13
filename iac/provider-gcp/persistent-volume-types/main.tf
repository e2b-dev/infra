
locals {
  nfs_version = var.nfs_version != "" ? var.nfs_version : (contains(["ZONAL", "REGIONAL", "ENTERPRISE"], var.tier) ? "4.1" : "3")
}

resource "google_filestore_instance" "persistent-volumes" {
  name     = "${var.prefix}persistent-volume-${var.key}"
  tier     = var.tier
  protocol = format("NFS_V%s", replace(local.nfs_version, ".", "_"))
  location = var.location

  deletion_protection_enabled = !(coalesce(var.allow_deletion, false))
  deletion_protection_reason  = "If this gets removed, the orchestrator will throw tons of errors"

  file_shares {
    capacity_gb = var.capacity_gb
    name        = var.key
  }

  networks {
    modes   = ["MODE_IPV4"]
    network = var.network_name
  }
}
