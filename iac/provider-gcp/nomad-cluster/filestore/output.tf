output "nfs_ip_addresses" {
  value = google_filestore_instance.shared_disk_store.networks[0].ip_addresses
}

output "nfs_version" {
  value = google_filestore_instance.shared_disk_store.protocol
}
