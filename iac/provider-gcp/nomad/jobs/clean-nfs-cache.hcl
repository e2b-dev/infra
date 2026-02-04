job "filestore-cleanup" {
    type = "batch"
    node_pool = "${node_pool}"

    datacenters = ["*"]

    periodic {
        cron             = "0 * * * *" // run every hour
        prohibit_overlap = true
        time_zone        = "America/Los_Angeles"
    }

    group "filestore-cleanup" {
        restart {
            attempts = 0
            mode     = "fail"
        }

        task "filestore-cleanup" {
          driver = "raw_exec"

          resources {
              memory = 2048 // in MB
          }

          env {
            NODE_ID = "$${node.unique.name}"
            %{ if launch_darkly_api_key != "" }
                LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
            %{ endif }
          }

          config {
                command = "local/clean-nfs-cache"
                args = [
                    "--dry-run=${dry_run}",
                    "--disk-usage-target-percent=${max_disk_usage_target}",
                    "--files-per-loop=${files_per_loop}",
                    "--deletions-per-loop=${deletions_per_loop}",
                    "--max-concurrent-stat=${max_concurrent_stat}",
                    "--max-concurrent-scan=${max_concurrent_scan}",
                    "--max-concurrent-delete=${max_concurrent_delete}",
                    "--max-retries=${max_retries}",
                    "--otel-collector-endpoint=${otel_collector_grpc_endpoint}",
                    "${nfs_cache_mount_path}",
                ]
            }

            artifact {
                %{ if environment == "dev" }
                // Version hash is only available for dev to increase development speed in prod use rolling updates
                source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/clean-nfs-cache?version=${clean_nfs_cache_checksum}"
                %{ else }
                source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/clean-nfs-cache"
                %{ endif }
              }

        }
    }
}
