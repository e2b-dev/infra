job "filestore-cleanup" {
    type = "batch"
    node_pool = "default"

    periodic {
        cron             = "0 * * * *" // run it once an hour, on the hour
        prohibit_overlap = true
        time_zone        = "America/Los_Angeles"
    }

    group "filestore-cleanup" {
        restart {
            attempts = 0
            mode     = "fail"
        }

        constraint {
            attribute = "$${meta.job_constraint}"
            value     = "${job_constraint_prefix}-${i + 1}"
        }

        task "filestore-cleanup" {
            driver = "raw_exec"

            config {
                command = "local/clean-nfs-cache"
                args = [
                    "--dry-run=true",
                    "--free-space-percent=90",
                    "${nfs_cache_mount_path}",
                ]
            }
        }
    }
}
