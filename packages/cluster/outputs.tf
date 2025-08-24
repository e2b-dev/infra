output "logs_proxy_ip" {
  value = module.network.logs_proxy_ip
}

output "shared_chunk_cache_path" {
  value = var.filestore_cache_enabled ? "${local.nfs_mount_path}/${local.nfs_mount_subdir}" : ""
}
