job "clickhouse-migrator" {
  type        = "batch"
  node_pool   = "${node_pool}"

%{ for i in range("${server_count}") }
  group "migrator-${i + 1}" {
    count = 1


    restart {
      interval         = "5m"
      attempts         = 5
      delay            = "15s"
      mode             = "delay"
    }

    constraint {
      attribute = "$${meta.job_constraint}"
      value     = "${job_constraint_prefix}-${i + 1}"
    }

    task "migrator" {
      driver = "docker"

      env {
        GOOSE_DBSTRING="clickhouse://${clickhouse_username}:${clickhouse_password}@localhost:${clickhouse_port}/default"
        GOOSE_DRIVER="clickhouse"
      }

      config {
        image = "${clickhouse_migrator_version}"
        network_mode = "host"
      }

      resources {
        cpu    = 250
        memory = 128
      }
    }
  }
%{ endfor }
}
