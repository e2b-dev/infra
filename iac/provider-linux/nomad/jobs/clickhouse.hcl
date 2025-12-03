job "clickhouse" {
  type      = "service"
  node_pool = "${node_pool}"

%{ for i in range("${server_count}") }
  group "server-${i + 1}" {
    count = 1

    restart {
      interval = "5m"
      attempts = 5
      delay    = "15s"
      mode     = "delay"
    }

    network {
      mode = "host"

      port "clickhouse-http" {
        static = 8123
        to     = 8123
      }

      port "clickhouse-metrics" {
        static = "${clickhouse_metrics_port}"
        to     = "${clickhouse_metrics_port}"
      }

      port "clickhouse-server" {
        static = "${clickhouse_server_port}"
        to     = "${clickhouse_server_port}"
      }
    }

    service {
      name = "clickhouse"
      port = "clickhouse-server"
      tags = ["server-${i + 1}"]

      check {
        type     = "http"
        path     = "/ping"
        port     = "clickhouse-http"
        interval = "10s"
        timeout  = "5s"
      }
    }

    task "clickhouse-server" {
      driver = "docker"

      env {
        CLICKHOUSE_USER = "${username}"
      }

      config {
%{ if docker_image_prefix != "" }
        image  = "${docker_image_prefix}/clickhouse/clickhouse-server:${clickhouse_version}"
%{ else }
        image  = "clickhouse/clickhouse-server:${clickhouse_version}"
%{ endif }
        ports  = ["clickhouse-server", "clickhouse-http"]
        ulimit { nofile = "262144:262144" }
        volumes = ["/clickhouse/data:/var/lib/clickhouse"]
      }

      resources {
        cpu    = ${cpu_count * 1000}
        memory = ${memory_mb}
      }
    }
  }
%{ endfor }
}
