job "logs-collector" {
  type      = "system"
  node_pool = "all"

  priority = 85

  group "logs-collector" {
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
        to = "${vector_health_port}"
      }

      port "logs" {
        to = "${vector_api_port}"
      }
    }

    service {
      name = "logs-collector"
      port = "logs"
      tags = [
        "logs",
        "health",
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = "${vector_health_port}"
      }
    }

    task "start-collector" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "timberio/vector:0.34.X-alpine"

        ports = [
          "health",
          "logs",
        ]
      }

      env {
        VECTOR_CONFIG          = "local/vector.toml"
        VECTOR_REQUIRE_HEALTHY = "true"
        VECTOR_LOG             = "warn"
      }

      resources {
        memory_max = 4096
        memory     = 512
        cpu        = 500
      }

      template {
        destination   = "local/vector.toml"
        change_mode   = "signal"
        change_signal = "SIGHUP"

        # overriding the delimiters to [[ ]] to avoid conflicts with Vector's native templating, which also uses {{ }}
        left_delimiter  = "[["
        right_delimiter = "]]"

        data            = <<EOH
${vector_config}
        EOH
      }
    }
  }
}
