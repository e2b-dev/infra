job "redis" {
  datacenters = ["${gcp_zone}"]
  node_pool = "${node_pool}"
  type = "service"
  priority = 95

  group "redis" {
    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "redis" {
        static = "${port_number}"
      }
    }

    service {
      name = "redis"
      port = "${port_name}"

      check {
        type     = "tcp"
        name     = "health"
        interval = "10s"
        timeout  = "2s"
        port     = "${port_number}"
      }
    }

    task "start" {
      driver = "docker"

      resources {
        memory_max = 4096
        memory     = 2048
        cpu        = 1000
      }

      config {
        network_mode = "host"
        image        = "redis:7.4.2-alpine"
        ports        = ["${port_name}"]
        args = [
        ]
      }
    }
  }
}
