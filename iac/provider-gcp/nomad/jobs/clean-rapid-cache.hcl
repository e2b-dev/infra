job "rapid-cache-cleanup" {
    type = "batch"
    node_pool = "${node_pool}"

    periodic {
        cron             = "0 * * * *"
        prohibit_overlap = true
        time_zone        = "America/Los_Angeles"
    }

    group "rapid-cache-cleanup" {
        restart {
            attempts = 0
            mode     = "fail"
        }

        task "rapid-cache-cleanup" {
          driver = "raw_exec"

          resources {
              memory = 512
          }

          env {
            RAPID_BUCKET_CACHE_BUCKET_NAME = "${bucket_name}"
            REDIS_URL                      = "${redis_url}"
            REDIS_CLUSTER_URL              = "${redis_cluster_url}"
            REDIS_TLS_CA_BASE64            = "${redis_tls_ca_base64}"
          }

          config {
                command = "local/clean-rapid-cache"
                args = [
                    "--dry-run=${dry_run}",
                    "--max-age=${max_age}",
                    "--max-deletions=${max_deletions}",
                    "${bucket_name}",
                ]
            }

            artifact {
                source      = "${artifact_source}"
                destination = "local/clean-rapid-cache"
                mode        = "file"
              }
        }
    }
}
