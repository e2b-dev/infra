job "otel-collector-nomad-server" {
  type        = "service"
  node_pool   = "${node_pool}"

  priority = 95

  group "otel-collector-nomad-server" {

    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "health" {
        to = 13134
      }
    }

    service {
      name = "otel-collector-nomad-server"

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = 13134
      }
    }

    task "start-collector-nomad-server" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "e2bdev/opentelemetry-collector-contrib:0.135.0-with-hugepages-metrics"

        volumes = [
          "local/config:/config",
        ]
        args = [
          "--config=local/config/otel-collector-config.yaml",
        ]

        ports = [
          "health",
        ]
      }

      resources {
        memory     = 512
        cpu        = 200
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
