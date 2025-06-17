job "clickhouse-backup" {
  type        = "batch"
  node_pool   = "${node_pool}"

  periodic {
    cron            = "0 2,8,14,20 * * *"
    prohibit_overlap = true
    time_zone       = "America/Los_Angeles"
  }


%{ for i in range("${server_count}") }
  group "backup-server-${i + 1}" {

     restart {
      attempts = 0
      mode     = "fail"
    }

    constraint {
      attribute = "$${meta.job_constraint}"
      value     = "${job_constraint_prefix}-${i + 1}"
    }

    task "clickhouse-backup" {
      driver = "docker"

      config {
        image = "altinity/clickhouse-backup:${clickhouse_backup_version}"
        network_mode = "host"

        volumes = [
          "/clickhouse/data:/var/lib/clickhouse",
        ]

        entrypoint = ["/bin/sh", "-c"]
        args = [
          "clickhouse-backup create_remote --tables='default.metrics_*' auto_backup_$(date +%F_%H-%M)"
        ]
      }

      env {
        CLICKHOUSE_HOST         = "server-${i + 1}.clickhouse.service.consul"
        CLICKHOUSE_PORT         = "${clickhouse_port}"
        CLICKHOUSE_USERNAME     = "${clickhouse_username}"
        CLICKHOUSE_PASSWORD     = "${clickhouse_password}"


        REMOTE_STORAGE               = "gcs"
        GCS_CREDENTIALS_JSON_ENCODED = "${gcs_credentials_json_encoded}"
        GCS_BUCKET                   = "${gcs_bucket}"
        GCS_PATH                     = "${gcs_folder}/backup/server-${i + 1}/"
      }

      resources {
        cpu = 200
        memory = 256
      }
    }
  }
%{ endfor }
}
