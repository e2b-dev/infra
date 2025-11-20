output "shared_chunk_cache_path" {
  value = var.filestore_cache_enabled ? "${local.nfs_mount_path}/${local.nfs_mount_subdir}" : ""
}

output "regional_lb_ip_address" {
  description = "IP address of the regional external load balancer for Nomad"
  value       = module.network.regional_lb_ip_address
}

output "regional_lb_certificate_pem" {
  description = "PEM-encoded self-signed certificate for the regional load balancer"
  value       = module.network.regional_lb_certificate_pem
  sensitive   = true
}
