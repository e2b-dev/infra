job "docker-reverse-proxy" {
  datacenters = ["${gcp_zone}"]
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
        POSTGRES_CONNECTION_STRING    = "${postgres_connection_string}"
        GOOGLE_SERVICE_ACCOUNT_BASE64 = "${google_service_account_secret}"
        GCP_REGION                    = "${gcp_region}"
        GCP_PROJECT_ID                = "${gcp_project_id}"
        GCP_DOCKER_REPOSITORY_NAME    = "${docker_registry}"
        DOMAIN_NAME                   = "${domain_name}"
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
