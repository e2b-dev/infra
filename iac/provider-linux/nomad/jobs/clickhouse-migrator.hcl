job "clickhouse-migrator" {
  type      = "batch"
  node_pool = "${node_pool}"

%{ for i in range("${server_count}") }
   group "migrator-${i + 1}" {
     count = 1

     restart {
       interval = "5m"
       attempts = 5
       delay    = "15s"
       mode     = "delay"
     }

    task "migrator" {
      driver = "docker"

      env {
        GOOSE_DBSTRING = "${clickhouse_connection_string}"
      }

      config {
        network_mode = "host"
%{ if clickhouse_migrator_image != "" }
        image = "${clickhouse_migrator_image}"
%{ else }
  %{ if docker_image_prefix != "" }
        image = "${docker_image_prefix}/clickhouse/clickhouse-migrator:latest"
  %{ else }
        image = "clickhouse/clickhouse-migrator:latest"
  %{ endif }
%{ endif }
      }

      resources {
        cpu    = 250
        memory = 128
      }
    }
  }
%{ endfor }
}
