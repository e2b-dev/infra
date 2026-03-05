output "nfs_version" {
  value = local.nfs_version
}

output "nfs_location" {
  value = format("%s:/%s",
    join(",", google_filestore_instance.persistent-volumes.networks[0].ip_addresses),
    var.key,
  )
}
