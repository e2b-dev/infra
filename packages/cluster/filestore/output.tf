output "nfs_ip_addresses" {
  value = google_filestore_instance.slab-cache.networks[0].ip_addresses
}
