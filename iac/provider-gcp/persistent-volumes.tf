resource "google_filestore_instance" "persistent-volumes" {
  for_each = var.persistent_volume_types

  name     = "${var.prefix}persistent-volume-${each.key}"
  tier     = each.value.tier
  location = each.value.location

  file_shares {
    capacity_gb = each.value.capacity_gb
    name        = each.key
  }

  networks {
    modes   = ["MODE_IPV4"]
    network = "default"
  }
}
