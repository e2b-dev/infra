job "filestore-cleanup" {
    type = "batch"
    node_pool = "${node_pool}"

    periodic {
        cron             = "0 * * * *" // run it once an hour, on the hour
        prohibit_overlap = true
        time_zone        = "America/Los_Angeles"
    }

%{ for i in range("${server_count}") }
    group "filestore-cleanup-${i + 1}" {
        restart {
            attempts = 0
            mode     = "fail"
        }

        constraint {
            attribute = "$${meta.job_constraint}"
            value     = "${job_constraint_prefix}-${i + 1}"
        }

        task "filestore-cleanup" {
            driver = "docker"

            config {
                image = "<< todo: add this >>"
                network_mode = "host"

                volumes = [
                    "<< todo: add this >>"
                ]

                entrypoint = ["<< todo: add this >>"]
                args = ["<< todo: add this >>"]

                env {
                    << add env vars >>
                }

                resources {
                    cpu    = 200
                    memory = 256
                }
            }
        }
    }
%{ endfor }
}
