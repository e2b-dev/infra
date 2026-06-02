job "api" {
  node_pool = "${node_pool}"
  priority = 90

  group "api-service" {
    count = ${count}

    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "api" {
        static = "${port_number}"
      }

      port "api_internal_grpc" {
        static = "${api_internal_grpc_port}"
      }

      port "grpc_api" {}

      %{ if prevent_colocation }
      port "scheduling-block" {
        // This port is used to block scheduling of jobs with the same block on the same node.
        // We use this to block API and Loki from being scheduled on the same node.
        static = 40234
      }
      %{ endif }
    }

    constraint {
      operator  = "distinct_hosts"
      value     = "true"
    }

    service {
      name = "api"
      port = "${port_number}"
      task = "start"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.api.entrypoints=web",

        "traefik.http.routers.api.rule=HostRegexp(`api.{domain:.+}`)",
        "traefik.http.routers.api.ruleSyntax=v2",
        "traefik.http.routers.api.priority=500"
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "${port_number}"
      }
    }

    service {
      name = "api-internal-grpc"
      port = "api_internal_grpc"
      task = "start"

      check {
        type     = "tcp"
        name     = "api-internal-grpc"
        interval = "3s"
        timeout  = "3s"
        port     = "api_internal_grpc"
      }
    }

    service {
      name = "grpc-api"
      port = "grpc_api"
      task = "start"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.grpc-api.entrypoints=web",
        "traefik.http.routers.grpc-api.rule=HostRegexp(`grpc-api.{domain:.+}`)",
        "traefik.http.routers.grpc-api.ruleSyntax=v2",
        "traefik.http.routers.grpc-api.priority=500",
        "traefik.http.routers.grpc-api.service=grpc-api",
        "traefik.http.services.grpc-api.loadbalancer.server.scheme=h2c"
      ]

      check {
        type     = "tcp"
        name     = "grpc-api"
        interval = "3s"
        timeout  = "3s"
        port     = "grpc_api"
      }
    }

    # Compatibility alias for service name `api-grpc`, which was renamed to `api-internal-grpc` in #2470.
    # Old client-proxy allocations were rendered with API_GRPC_ADDRESS=api-grpc.service.consul:<port> and still expect that name.
    # Drop this block once all old client-proxy allocations have been replaced.
    service {
      name = "api-grpc"
      port = "api_internal_grpc"
      task = "start"

      check {
        type     = "tcp"
        name     = "api-grpc"
        interval = "3s"
        timeout  = "3s"
        port     = "api_internal_grpc"
      }
    }

%{ if update_stanza }
    # An update stanza to enable rolling updates of the service
    update {
      # The number of extra instances to run during the update
      max_parallel      = 1
      # Allows to spawn new version of the service before killing the old one
      canary            = 1

      # Time the canary must stay healthy (in Consul) before it is promoted and
      # the old allocations are stopped. Intentionally long: API traffic is
      # routed by the GCP LB directly to allocations via a fixed-port health
      # check, and a canary on a freshly created MIG node takes ~60s to be
      # admitted as a routable backend. A short value promotes and tears down
      # the old (still GCP-routable) backends before the new node is admitted,
      # leaving the LB with zero healthy backends -> 503 failed_to_pick_backend.
      min_healthy_time  = "120s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline  = "10800s"
      # Time to wait for the overall update to complete. Otherwise, the deployment is marked as failed and rolled back
      # This is on purpose very tight, we want to fail immediately if the deployment is marked as unhealthy
      progress_deadline = "10801s"
      # Whether to promote the canary if the rest of the group is not healthy
      auto_promote      = true
      # Whether to automatically rollback if the update fails
      auto_revert       = true
    }
%{ endif }

    task "start" {
      driver       = "docker"
      # Budget = shutdownDrainWait (15s) + shutdownTimeout (requestTimeout 70s + 5s) + cleanup (30s) + slack.
      # https://developer.hashicorp.com/nomad/docs/configuration/client#max_kill_timeout
      kill_timeout = "150s"
      kill_signal  = "SIGTERM"

      resources {
        memory_max = ${memory_mb * 2}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        NODE_ID                        = "$${node.unique.id}"
        API_EDGE_GRPC_PORT             = "$${NOMAD_PORT_grpc_api}"

%{ for key, value in job_env_vars ~}
        ${key} = "${value}"
%{ endfor ~}
      }

      config {
        network_mode = "host"
        image        = "${api_docker_image}"
        ports        = ["${port_name}", "grpc_api"]
        args         = [
          "--port", "${port_number}",
        ]
      }
    }

    task "db-migrator" {
      driver = "docker"

      env {
%{ for key, value in db_migrator_env_vars ~}
        ${key} = "${value}"
%{ endfor ~}
      }

      config {
        image = "${db_migrator_docker_image}"
      }

      resources {
        cpu    = 250
        memory = 128
      }

      lifecycle {
        hook = "prestart"
        sidecar = false
      }
    }
  }
}
