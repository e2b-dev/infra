job "dashboard-api" {
  node_pool = "${node_pool}"
  priority = 80

  group "dashboard-api-service" {
    count = ${count}

    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "api" {}
    }

    service {
      name = "dashboard-api"
      port = "api"
      task = "start"

      tags = [

        "traefik.enable=true",

        "traefik.http.routers.dashboard-api.rule=HostRegexp(`${subdomain}.{domain:.+}`)",
        "traefik.http.routers.dashboard-api.ruleSyntax=v2",
        "traefik.http.routers.dashboard-api.priority=1000"
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "api"
      }
    }

%{ if update_stanza }
    # An update stanza to enable rolling updates of the service
    update {
      # The number of extra instances to run during the update
      max_parallel      = 1
      # Allows to spawn new version of the service before killing the old one
      canary            = 1
      # Time to wait for the canary to be healthy
      min_healthy_time  = "10s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline  = "900s"
      # Time to wait for the overall update to complete. Otherwise, the deployment is marked as failed and rolled back
      # This is on purpose very tight, we want to fail immediately if the deployment is marked as unhealthy
      progress_deadline = "901s"
      # Whether to promote the canary if the rest of the group is not healthy
      auto_promote      = true
    }
%{ endif }

    task "start" {
      driver       = "docker"
      kill_timeout = "30s"
      kill_signal  = "SIGTERM"

      resources {
        memory_max = ${memory_mb * 2}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        GIN_MODE                               = "release"
        ENVIRONMENT                            = "${environment}"
        NODE_ID                                = "$${node.unique.id}"
        PORT                                   = "$${NOMAD_PORT_api}"
        POSTGRES_CONNECTION_STRING             = "${postgres_connection_string}"
        AUTH_DB_CONNECTION_STRING              = "${auth_db_connection_string}"
        AUTH_DB_READ_REPLICA_CONNECTION_STRING = "${auth_db_read_replica_connection_string}"
        CLICKHOUSE_CONNECTION_STRING           = "${clickhouse_connection_string}"
        SUPABASE_JWT_SECRETS                   = "${supabase_jwt_secrets}"
        SUPABASE_AUTH_USER_SYNC_ENABLED        = "${supabase_auth_user_sync_enabled}"
        OTEL_COLLECTOR_GRPC_ENDPOINT           = "${otel_collector_grpc_endpoint}"
        LOGS_COLLECTOR_ADDRESS                 = "${logs_collector_address}"
      }

      config {
        network_mode = "host"
        image        = "${image_name}"
        ports        = ["api"]
      }
    }
  }
}
