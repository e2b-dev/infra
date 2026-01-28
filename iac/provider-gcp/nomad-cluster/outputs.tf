output "shared_chunk_cache_path" {
  value = var.filestore_cache_enabled ? "${local.nfs_mount_path}/${local.nfs_mount_subdir}" : ""
}

output "persistent_volumes_bucket" {
  value = module.shared-persistence.bucket_name
}
