job "client-proxy" {
  datacenters = ["${gcp_zone}"]
  node_pool = "api"

  priority = 80

  group "client-proxy" {
  count = ${count}

  constraint {
    operator  = "distinct_hosts"
    value     = "true"
  }

    network {
      port "${proxy_port_name}" {
        static = "${proxy_port}"
      }

      port "${api_port_name}" {
        static = "${api_port}"
      }
    }

    service {
      name = "proxy"
      port = "${proxy_port_name}"

      check {
        type     = "http"
        name     = "health"
        path     = "/health/traffic"
        interval = "3s"
        timeout  = "3s"
        port     = "${api_port_name}"
      }
    }

    service {
      name = "edge-api"
      port = "${api_port}"

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "${api_port_name}"
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
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        NODE_ID = "$${node.unique.id}"
        NODE_IP = "$${attr.unique.network.ip-address}"

        EDGE_PORT         = "${api_port}"
        EDGE_SECRET       = "${api_secret}"
        PROXY_PORT        = "${proxy_port}"
        ORCHESTRATOR_PORT = "${orchestrator_port}"

        SERVICE_DISCOVERY_ORCHESTRATOR_PROVIDER             = "DNS"
        SERVICE_DISCOVERY_ORCHESTRATOR_DNS_RESOLVER_ADDRESS = "127.0.0.1:8600" // consul dns resolver
        SERVICE_DISCOVERY_ORCHESTRATOR_DNS_QUERY            = "orchestrator.service.consul,template-manager.service.consul"

        SERVICE_DISCOVERY_EDGE_PROVIDER             = "DNS"
        SERVICE_DISCOVERY_EDGE_DNS_RESOLVER_ADDRESS = "127.0.0.1:8600" // consul dns resolver
        SERVICE_DISCOVERY_EDGE_DNS_QUERY            = "edge-api.service.consul"

        ENVIRONMENT = "${environment}"

        // use legacy dns resolution for orchestrator services
        USE_PROXY_CATALOG_RESOLUTION = "false"

        OTEL_COLLECTOR_GRPC_ENDPOINT  = "${otel_collector_grpc_endpoint}"
        LOGS_COLLECTOR_ADDRESS        = "${logs_collector_address}"
        REDIS_URL                     = "${redis_url}"
        LOKI_URL                      = "${loki_url}"

        %{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
        %{ endif }
      }

      config {
        network_mode = "host"
        image        = "${image_name}"
        ports        = ["${proxy_port_name}", "${api_port_name}"]
      }
    }
  }
}
