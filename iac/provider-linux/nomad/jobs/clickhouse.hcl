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
        CLICKHOUSE_USER            = "${username}"
        CLICKHOUSE_PASSWORD        = "${password}"
        CLICKHOUSE_SKIP_USER_SETUP = "1"
      }

      config {
%{ if dockerhub_remote_repository_url != "" }
        image  = "${dockerhub_remote_repository_url}/clickhouse/clickhouse-server:${clickhouse_version}"
%{ else }
        image  = "clickhouse/clickhouse-server:${clickhouse_version}"
%{ endif }
        network_mode = "host"
        ulimit { nofile = "262144:262144" }

        ports = ["clickhouse-server", "clickhouse-http"]

        extra_hosts = [
          "server-${i + 1}.clickhouse.service.consul:127.0.0.1",
        ]

        volumes = [
          "local/config.xml:/etc/clickhouse-server/config.d/config.xml:ro",
          "local/users.xml:/etc/clickhouse-server/users.xml:ro",
          "local/client-config.xml:/etc/clickhouse-client/config.d/client-config.xml:ro"
        ]
      }

      template {
        destination = "local/client-config.xml"
        data        = <<EOF
${clickhouse_client_config}
EOF
      }

      template {
        destination = "local/config.xml"
        data        = <<EOF
${clickhouse_config}
EOF
      }

      template {
        destination = "local/users.xml"
        data        = <<EOF
${clickhouse_users_config}
EOF
      }

      resources {
        cpu    = ${cpu_count * 1000}
        memory = ${memory_mb}
      }
    }
  }
%{ endfor }
}
