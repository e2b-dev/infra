job "client-proxy" {
  datacenters = ["${gcp_zone}"]
  node_pool = "api"

  priority = 80

  group "client-proxy" {
    network {
      port "${port_name}" {
        static = "${port_number}"
      }

      port "health" {
        static = "${health_port_number}"
      }
    }

    service {
      name = "proxy"
      port = "${port_name}"


      check {
        type     = "http"
        name     = "health"
        path     = "/"
        interval = "20s"
        timeout  = "5s"
        port     = "health"
      }
    }

%{ if update_stanza }
    # An update stanza to enable rolling updates of the service
    update {
      # The number of extra instances to run during the update
      max_parallel     = 1
      # Allows to spawn new version of the service before killing the old one
      canary           = 1
      # Time to wait for the canary to be healthy
      min_healthy_time = "10s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline = "30s"
      # Whether to promote the canary if the rest of the group is not healthy
      auto_promote     = true
      # Deadline for the update to be completed
      progress_deadline = "24h"
    }
%{ endif }

    task "start" {
      driver = "docker"
      # If we need more than 30s we will need to update the max_kill_timeout in nomad
      # https://developer.hashicorp.com/nomad/docs/configuration/client#max_kill_timeout
%{ if update_stanza }
      kill_timeout = "24h"
%{ endif }
      kill_signal  = "SIGTERM"

      resources {
        memory_max = 4096
        memory     = 1024
        cpu        = 1000
      }

      env {
        OTEL_COLLECTOR_GRPC_ENDPOINT  = "${otel_collector_grpc_endpoint}"
      }

      config {
        network_mode = "host"
        image        = "${image_name}"
        ports        = ["${port_name}"]
        args         = ["--port", "${port_number}"]
      }
    }
  }
}
