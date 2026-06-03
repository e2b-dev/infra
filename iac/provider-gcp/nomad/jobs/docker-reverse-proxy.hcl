job "docker-reverse-proxy" {
  node_pool   = "${node_pool}"
  type        = "service"
  priority    = 85

  group "reverse-proxy" {
    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "${port_name}" {
        static = "${port_number}"
      }
    }

    service {
      name = "docker-reverse-proxy"
      port = "${port_name}"

      check {
        type     = "http"
        name     = "health"
        path     = "${health_check_path}"
        interval = "20s"
        timeout  = "5s"
        port     = "${port_number}"
      }
    }

    task "start" {
      driver = "docker"

      resources {
        memory_max = 2048
        memory     = 512
        cpu        = 256
      }

      env {
%{ for key, value in job_env_vars ~}
        ${key} = "${value}"
%{ endfor ~}
      }

      config {
        network_mode = "host"
        image        = "${image_name}"
        ports        = ["${port_name}"]
        args = [
          "--port", "${port_number}",
        ]
      }
    }
  }
}
