output "shared_chunk_cache_path" {
  value = var.filestore_cache_enabled ? "${local.nfs_mount_path}/${local.nfs_mount_subdir}" : ""
}

output "default_client_managed_instance_group_name" {
  description = "Name of the default client worker MIG; the scale-out controller's only resize target."
  value       = try(basename(module.client_cluster["default"].network_hardening_stage_ready.managed_group), "")
}
