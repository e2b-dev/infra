data "google_storage_bucket_object" "filestore_cleanup" {
  name   = "clean-nfs-cache"
  bucket = var.fc_env_pipeline_bucket_name
}

data "external" "filestore_cleanup_checksum" {
  program = ["bash", "${path.module}/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.filestore_cleanup.md5hash
  }
}

resource "nomad_job" "clean_nfs_cache" {
  count = var.shared_chunk_cache_path != "" ? 1 : 0

  jobspec = templatefile("${path.module}/clean-nfs-cache.hcl", {
    bucket_name              = var.fc_env_pipeline_bucket_name
    environment              = var.environment
    clean_nfs_cache_checksum = data.external.filestore_cleanup_checksum.result.hex
    nfs_cache_mount_path     = var.shared_chunk_cache_path
    max_disk_usage_target    = var.filestore_cache_max_disk_usage_target
  })
}
