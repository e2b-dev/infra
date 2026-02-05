data "google_storage_bucket_object" "filestore_cleanup" {
  name   = "clean-nfs-cache"
  bucket = var.fc_env_pipeline_bucket_name
}

data "external" "filestore_cleanup_checksum" {
  program = ["bash", "${path.module}/scripts/checksum.sh"]

  query = {
    base64 = data.google_storage_bucket_object.filestore_cleanup.md5hash
  }
}

resource "nomad_job" "clean_nfs_cache" {
  count = var.shared_chunk_cache_path != "" ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/clean-nfs-cache.hcl", {
    node_pool                    = var.builder_node_pool
    bucket_name                  = var.fc_env_pipeline_bucket_name
    environment                  = var.environment
    clean_nfs_cache_checksum     = data.external.filestore_cleanup_checksum.result.hex
    nfs_cache_mount_path         = var.shared_chunk_cache_path
    max_disk_usage_target        = var.filestore_cache_cleanup_disk_usage_target
    dry_run                      = var.filestore_cache_cleanup_dry_run
    deletions_per_loop           = var.filestore_cache_cleanup_deletions_per_loop
    files_per_loop               = var.filestore_cache_cleanup_files_per_loop
    max_concurrent_stat          = var.filestore_cache_cleanup_max_concurrent_stat
    max_concurrent_scan          = var.filestore_cache_cleanup_max_concurrent_scan
    max_concurrent_delete        = var.filestore_cache_cleanup_max_concurrent_delete
    max_retries                  = var.filestore_cache_cleanup_max_retries
    otel_collector_grpc_endpoint = "localhost:${var.otel_collector_grpc_port}"
    launch_darkly_api_key        = trimspace(data.google_secret_manager_secret_version.launch_darkly_api_key.secret_data)
  })
}
