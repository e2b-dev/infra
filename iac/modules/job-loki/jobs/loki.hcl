job "loki" {
  type      = "service"
  node_pool = "${node_pool}"
  priority  = 75

  group "loki-service" {
    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "loki" {
        to = "${loki_port}"
      }

%{ if prevent_colocation }
      port "scheduling-block" {
        // This port is used to block scheduling of jobs with the same block on the same node.
        // We use this to block API and Loki from being scheduled on the same node.
        static = 40234
      }
%{ endif }
    }

    service {
      name = "loki"
      port = "loki"

      check {
        type     = "http"
        path     = "/ready"
        interval = "20s"
        timeout  = "2s"
        port     = "${loki_port}"
      }
    }

    task "loki" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "${loki_image}"

        args = [
          "-config.file",
          "local/loki-config.yml",
        ]
      }

      resources {
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      template {
        data        = <<EOF
${loki_config}
EOF
        destination = "local/loki-config.yml"
      }
    }
  }
}
