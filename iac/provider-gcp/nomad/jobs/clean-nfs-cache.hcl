job "filestore-cleanup" {
    type = "batch"
    node_pool = "${node_pool}"

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
            OTEL_COLLECTOR_GRPC_ENDPOINT = "${otel_collector_grpc_endpoint}"
%{ for key, value in job_env_vars ~}
            ${key} = "${value}"
%{ endfor ~}
          }

          config {
                command = "local/clean-nfs-cache"
                // Remaining knobs (grace, sampling, verify) use their built-in
                // defaults and are tuned live via the LaunchDarkly clean-nfs-cache flag.
                args = [
                    "--dry-run=${dry_run}",
                    "--disk-usage-target-percent=${max_disk_usage_target}",
                    "--max-concurrent-stat=${max_concurrent_stat}",
                    "--max-concurrent-scan=${max_concurrent_scan}",
                    "--max-concurrent-delete=${max_concurrent_delete}",
                    "${nfs_cache_mount_path}",
                ]
            }

            artifact {
                source      = "${artifact_source}"
                destination = "local/clean-nfs-cache"
                mode        = "file"
              }

        }
    }
}
