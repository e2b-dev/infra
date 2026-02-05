job "client-proxy" {
  node_pool = "${node_pool}"
  priority  = 80

  group "client-proxy" {
    // If the service fails, try up to 2 restarts in 10 minutes
    // if another restart happens, it will trigger reschedule
    restart {
      attempts = 2
      interval = "10m"
      delay    = "10s"
      mode     = "fail"
    }

    // If too many restarts happens on one node,
    // try to place it on another with exponential backoff
    reschedule {
      delay          = "30s"
      delay_function = "exponential"
      max_delay      = "10m"
      unlimited      = true
    }

    count = ${count}

    constraint {
      operator  = "distinct_hosts"
      value     = "true"
    }

    network {
      port "proxy" {
        static = "${proxy_port}"
      }

      port "health" {
        static = "${health_port}"
      }
    }

    service {
      name = "client-proxy"
      port = "proxy"

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "health"
      }
    }

%{ if update_stanza }
    # An update stanza to enable rolling updates of the service
    update {
      # The number of instances that can be updated at the same time
      max_parallel     = ${update_max_parallel}
      # Number of extra instances that can be spawn before killing the old one
      canary           = ${update_max_parallel}
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
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        NODE_ID = "$${node.unique.id}"
        NODE_IP = "$${attr.unique.network.ip-address}"

        HEALTH_PORT = "${health_port}"
        PROXY_PORT  = "${proxy_port}"

        ENVIRONMENT = "${environment}"

        OTEL_COLLECTOR_GRPC_ENDPOINT = "${otel_collector_grpc_endpoint}"
        LOGS_COLLECTOR_ADDRESS       = "${logs_collector_address}"

        REDIS_URL           = "${redis_url}"
        REDIS_CLUSTER_URL   = "${redis_cluster_url}"
        REDIS_TLS_CA_BASE64 = "${redis_tls_ca_base64}"

        %{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
        %{ endif }
      }

      config {
        network_mode = "host"
        image        = "${image_name}"
        ports        = ["proxy", "health"]
      }
    }
  }
}
