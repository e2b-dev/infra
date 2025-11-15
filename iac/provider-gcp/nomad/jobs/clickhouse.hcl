job "clickhouse" {
  type        = "service"
  node_pool   = "${node_pool}"

%{ for i in range("${server_count}") }
  group "server-${i + 1}" {
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

    network {
      mode = "bridge"

      dns {
        servers = ["172.17.0.1", "8.8.8.8", "8.8.4.4", "169.254.169.254"]
      }

      port "clickhouse-http" {
        static = 8123
        to = 8123
      }

      port "clickhouse-metrics" {
        static = "${clickhouse_metrics_port}"
        to = "${clickhouse_metrics_port}"
      }

      port "clickhouse-server" {
        static = "${clickhouse_server_port}"
        to = "${clickhouse_server_port}"
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
           CLICKHOUSE_USER="${username}"
      }

      config {
        image = "clickhouse/clickhouse-server:${clickhouse_version}"
        ports = ["clickhouse-server", "clickhouse-http"]

        ulimit {
          nofile = "262144:262144"
        }

        extra_hosts = [
          "server-${i + 1}.clickhouse.service.consul:127.0.0.1",
        ]

        volumes = [
          "/clickhouse/data:/var/lib/clickhouse",
          "local/config.xml:/etc/clickhouse-server/config.d/config.xml",
          "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
        ]
      }

      resources {
        cpu    = ${cpu_count * 1000}
        memory = ${memory_mb}
      }

      template {
        destination = "local/config.xml"
        data        =<<EOF
${clickhouse_config}
EOF
      }

      template {
        destination = "local/users.xml"
        data        =<<EOF
${clickhouse_users_config}
EOF
      }
    }

    task "otel-collector" {
      driver = "docker"

      config {
        network_mode = "host"

        image = "otel/opentelemetry-collector-contrib:0.123.0"
        args = [
          "--config=local/otel.yaml",
          "--feature-gates=pkg.translator.prometheus.NormalizeName",
        ]
      }

      resources {
        cpu    = 250
        memory = 128
      }

      template {
        data        =<<EOF
${otel_agent_config}
EOF
        destination = "local/otel.yaml"
      }

      # Order the sidecar BEFORE the app so it’s ready to receive traffic
      lifecycle {
        sidecar = "true"
        hook = "prestart"
      }
    }
  }
%{ endfor }
}
