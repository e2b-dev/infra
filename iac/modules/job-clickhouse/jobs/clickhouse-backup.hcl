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
          "clickhouse-backup create_remote --delete-local --tables='default.*' auto_backup_$(date +%F_%H-%M)"
        ]
      }

      env {
        CLICKHOUSE_HOST         = "localhost"
        CLICKHOUSE_PORT         = "${clickhouse_port}"
        CLICKHOUSE_USERNAME     = "${clickhouse_username}"
        CLICKHOUSE_PASSWORD     = "${clickhouse_password}"

%{ if cloud_provider == "gcp" }
        REMOTE_STORAGE               = "gcs"
        GCS_CREDENTIALS_JSON_ENCODED = "${gcs_credentials_json_encoded}"
        GCS_BUCKET                   = "${backup_bucket}"
        GCS_PATH                     = "${backup_folder}/backup/server-${i + 1}/"
%{ endif }
%{ if cloud_provider == "aws" }
        REMOTE_STORAGE = "s3"
        S3_BUCKET      = "${backup_bucket}"
        S3_REGION      = "${aws_region}"
        S3_PATH        = "${backup_folder}/backup/server-${i + 1}/"
%{ endif }
      }

      resources {
        cpu = 200
        memory = 256
      }
    }
  }
%{ endfor }
}
