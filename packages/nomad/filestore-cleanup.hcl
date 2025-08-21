job "filestore-cleanup" {
    type = "batch"
    node_pool = "default"

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

            config {
                command = "local/clean-nfs-cache"
                args = [
                    "--dry-run=true",
                    "--disk-usage-target-percent=80",
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
