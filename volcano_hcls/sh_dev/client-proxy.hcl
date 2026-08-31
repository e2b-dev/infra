job "client-proxy" {
  datacenters = ["dev-e2b-dc"]
  node_pool = "api"
  priority  = 80

  meta {
    force_redeploy = "v1"
  }

  group "client-proxy" {
    count = 1

    #constraint {
    #  operator = "distinct_hosts"
    #  value    = "true"
    #}

    constraint {
      attribute = "${meta.role}"
      value     = "api"
      # attribute = "${node.unique.id}"
      # value     = "d8b475a4-da45-7475-493d-a1fe8303da30"
    }

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

    network {
      port "health" {
        static = 3001
      }
      port "proxy" {
        static = 3002
      }
    }

    service {
      name = "client-proxy"
      port = "proxy"

      // This route is fallback (with lowest priority) to catch all requests as it serves sandbox traffic with dynamic subdomains
      tags = [
        "traefik.enable=true",

        "traefik.http.routers.client-proxy.entrypoints=web",
        "traefik.http.routers.client-proxy.rule=PathPrefix(`/`)",
        "traefik.http.routers.client-proxy.ruleSyntax=v2",
        "traefik.http.routers.client-proxy.priority=100",

        "traefik.http.services.client-proxy.loadbalancer.server.port=${NOMAD_PORT_proxy}"
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "health"
      }
    }

    # An update stanza to enable rolling updates of the service
    update {
      # The number of instances that can be updated at the same time
      max_parallel = 1
      # Number of extra instances that can be spawn before killing the old one
      canary = 0
      # Time to wait for the canary to be healthy
      min_healthy_time = "10s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline = "30s"
      # Whether to promote the canary if the rest of the group is not healthy
      #auto_promote = true
      auto_revert = true
      # Deadline for the update to be completed
      progress_deadline = "24h"
    }

    task "start" {
      driver = "docker"
      # If we need more than 30s we will need to update the max_kill_timeout in nomad
      # https://developer.hashicorp.com/nomad/docs/configuration/client#max_kill_timeout
      kill_timeout = "24h"
      kill_signal  = "SIGTERM"

      resources {
        memory_max = 4096
        memory     = 2048
        cpu        = 2000
      }

      env {
        NODE_ID = "${node.unique.id}"
        NODE_IP = "${attr.unique.network.ip-address}"

        HEALTH_PORT = "${NOMAD_PORT_health}"
        PROXY_PORT  = "${NOMAD_PORT_proxy}"

        ENVIRONMENT = "dev"

        REDIS_URL           = "192.168.162.32:6379"
        REDIS_CLUSTER_URL   = ""
        REDIS_POOL_SIZE     = "100"
        REDIS_TLS_CA_BASE64 = ""

        OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:4317"
        LOGS_COLLECTOR_ADDRESS       = "http://localhost:19095"

        # used by in-cluster client-proxy to call API ResumeSandbox over gRPC
        API_INTERNAL_GRPC_ADDRESS = "api-internal-grpc.service.consul:5009"

        LAUNCH_DARKLY_API_KEY = ""
      }

      config {
        network_mode = "host"
        image        = "mp-bp-cn-shanghai.cr.volces.com/e2b/client-proxy:2026.29"
        ports        = ["health", "proxy"]
      }
    }
  }
}