job "otel-collector" {
  type        = "system"
  node_pool   = "all"

  priority = 95

  group "otel-collector" {
    network {
      port "health" {
        to = 13133
      }

      port "metrics" {
        to = 8888
      }

      # Receivers
      port "grpc" {
        to = ${otel_collector_grpc_port}
      }

      port "http" {
        to = 4318
      }
    }

    service {
      name = "otel-collector"
      port = "grpc"
      tags = ["grpc"]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = 13133
      }
    }

    task "start-collector" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "otel/opentelemetry-collector-contrib:0.123.0"

        volumes = [
          "local/config:/config",
        ]
        args = [
          "--config=local/config/otel-collector-config.yaml",
          "--feature-gates=pkg.translator.prometheus.NormalizeName",
        ]

        ports = [
          "metrics",
          "grpc",
          "health",
          "http",
        ]
      }

      resources {
        memory     = 4096
        cpu        = 2000
      }

      template {
        data        =  <<EOF
${otel_collector_config}
EOF
        destination = "local/config/otel-collector-config.yaml"
      }
    }
  }
}