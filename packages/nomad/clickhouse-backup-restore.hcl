job "clickhouse-backup-restore" {
  type        = "batch"
  node_pool   = "${node_pool}"

  parameterized {
    meta_required = ["backup_name"]
  }

%{ for i in range("${server_count}") }
  group "backup-restore-server-${i + 1}" {

     restart {
      attempts = 0
      mode     = "fail"
    }

    constraint {
      attribute = "$${meta.job_constraint}"
      value     = "${job_constraint_prefix}-${i + 1}"
    }

    task "clickhouse-backup-restore" {
      driver = "docker"

      restart {
        attempts = 0
        mode     = "fail"
      }

      config {
        image = "altinity/clickhouse-backup:${clickhouse_backup_version}"
        network_mode = "host"

        volumes = [
          "/clickhouse/data:/var/lib/clickhouse",
        ]

        entrypoint = ["/bin/sh", "-c"]
        args = [
          "clickhouse-backup restore_remote --tables='default.metrics_*' $NOMAD_META_backup_name"
        ]
      }

      env {
        CLICKHOUSE_HOST         = "server-${i + 1}.clickhouse.service.consul"
        CLICKHOUSE_PORT         = "${clickhouse_port}"
        CLICKHOUSE_USERNAME     = "${clickhouse_username}"
        CLICKHOUSE_PASSWORD     = "${clickhouse_password}"


        REMOTE_STORAGE               = "gcs"
        GCS_DEBUG                    = "true"
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
