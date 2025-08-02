output "logs_proxy_ip" {
  value = module.network.logs_proxy_ip
}

output "nfs_slab_cache_path" {
  value = "${local.nfs_mount_path}/${local.nfs_mount_subdir}"
}
