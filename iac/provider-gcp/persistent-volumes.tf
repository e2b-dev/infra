module "persistent-volume-types" {
  source = "./persistent-volume-types"

  for_each = var.persistent_volume_types

  allow_deletion = each.value.allow_deletion
  capacity_gb    = each.value.capacity_gb
  key            = each.key
  location       = each.value.location
  network_name   = var.network_name
  nfs_version    = each.value.nfs_version
  prefix         = var.prefix
  tier           = each.value.tier
}
