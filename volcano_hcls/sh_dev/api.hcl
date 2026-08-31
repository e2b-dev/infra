job "api" {
  type        = "service"
  datacenters = ["dev-e2b-dc"]
  node_pool   = "api"
  priority    = 90

  meta {
    force_redeploy = "v1"
  }

  group "api-service" {
    count = 1

    constraint {
      attribute = "${meta.role}"
      value     = "api"
      # attribute = "${node.unique.id}"
      # value     = "d8b475a4-da45-7475-493d-a1fe8303da30"
    }

    restart {
      interval = "5s"
      attempts = 1
      delay    = "5s"
      mode     = "delay"
    }

    network {
      port "api" {
        static = 3000
      }
      port "api_internal_grpc" {
        static = 5009
      }
      port "grpc_api" {}
    }

    service {
      name = "api"
      port = "api"
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
        port     = "api"
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

    # An update stanza to enable rolling updates of the service
    update {
      # The number of extra instances to run during the update
      max_parallel = 1
      # Allows to spawn new version of the service before killing the old one
      canary = 0
      # Time to wait for the canary to be healthy
      min_healthy_time = "10s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline = "10800s"
      # Time to wait for the overall update to complete. Otherwise, the deployment is marked as failed and rolled back
      # This is on purpose very tight, we want to fail immediately if the deployment is marked as unhealthy
      progress_deadline = "10801s"
      # Whether to promote the canary if the rest of the group is not healthy
      # auto_promote = true
      # Whether to automatically rollback if the update fails
      auto_revert = true
    }

    task "start" {
      driver       = "docker"
      kill_timeout = "150s"
      kill_signal  = "SIGTERM"

      resources {
        memory_max = 4096
        memory     = 2048
        cpu        = 2000
      }

      env {
        ENVIRONMENT                    = "dev"
        DOMAIN_NAME                    = ""
        NODE_ID                        = "${node.unique.id}"
        NOMAD_TOKEN                    = "2fd71dab-2dae-e4e2-9996-ff41451ec77f"
        E2B_DEBUG                      = "true"
        ORCHESTRATOR_PORT              = "9090"
        API_INTERNAL_GRPC_PORT         = "5009"
        API_EDGE_GRPC_PORT             = "${NOMAD_PORT_grpc_api}"
        ADMIN_TOKEN                    = "dev-admin-token-change-in-production"
        SANDBOX_ACCESS_TOKEN_HASH_SEED = "dev-random-seed-change-in-production"

        POSTGRES_CONNECTION_STRING             = "postgresql://e2b:Galaxy123@192.168.162.33:5432/dev-e2b-pg?sslmode=disable"
        DB_MAX_OPEN_CONNECTIONS                = "100"
        DB_MIN_IDLE_CONNECTIONS                = "10"
        AUTH_DB_CONNECTION_STRING              = "postgresql://e2b:Galaxy123@192.168.162.33:5432/dev-e2b-pg?sslmode=disable"
        AUTH_DB_READ_REPLICA_CONNECTION_STRING = "postgresql://e2b:Galaxy123@192.168.162.33:5432/dev-e2b-pg?sslmode=disable"
        AUTH_DB_MAX_OPEN_CONNECTIONS           = "100"
        AUTH_DB_MIN_IDLE_CONNECTIONS           = "10"

        # OIDC JWT 验证: auth.xiaobei.top (Ory Hydra) + legacy HMAC
        AUTH_PROVIDER_CONFIG = "{\"jwt\":[{\"issuer\":{\"url\":\"https://auth.xiaobei.top\",\"audiences\":[\"e2b-sh-dev\"]},\"cacheDuration\":\"5m\"}],\"legacy\":{\"hmac\":{\"secrets\":[\"xiaobei-dashboard-jwt-secret-2026-dev\"]}}}"

        CLICKHOUSE_CONNECTION_STRING = "clickhouse://default:@192.168.162.30:9000/dev_e2b_clickhouse"

        REDIS_URL           = "192.168.162.32:6379"
        REDIS_CLUSTER_URL   = ""
        REDIS_POOL_SIZE     = "100"
        REDIS_TLS_CA_BASE64 = ""

        POSTHOG_API_KEY               = ""
        ANALYTICS_COLLECTOR_HOST      = ""
        ANALYTICS_COLLECTOR_API_TOKEN = ""
        LOGS_COLLECTOR_ADDRESS        = "http://localhost:19095"
        OTEL_COLLECTOR_GRPC_ENDPOINT  = "localhost:4317"
        LOKI_URL                      = "http://localhost:3100"

        LAUNCH_DARKLY_API_KEY = ""

        TEMPLATE_BUCKET_NAME = "dev-template"
        STORAGE_PROVIDER     = "AWSBucket"
        AWS_ENDPOINT_URL     = "https://tos-s3-cn-shanghai.ivolces.com"
        # AWS_ENDPOINT_URL_S3 = "https://tos-s3-cn-shanghai.volces.com"
        AWS_REGION            = "cn-shanghai"
        AWS_ACCESS_KEY_ID     = "REPLACE_WITH_VOLC_AK"
        AWS_SECRET_ACCESS_KEY = "REPLACE_WITH_VOLC_SK"
        S3_USE_PATH_STYLE     = "false"


        VOLUME_TOKEN_ISSUER           = "local.e2b.dev"
        VOLUME_TOKEN_SIGNING_METHOD   = "ES256"
        VOLUME_TOKEN_SIGNING_KEY      = "ECDSA:LS0tLS1CRUdJTiBFQyBQUklWQVRFIEtFWS0tLS0tCk1IY0NBUUVFSUFna0FCZ000a0lIa0VPVWdTNTVZeldVTjRkV3k0WjY4R2c2TUpUTGFabkRvQW9HQ0NxR1NNNDkKQXdFSG9VUURRZ0FFbFFnQ3RnWnkrb3RoUDA5bk4yUWdVNjB6ekxNaW9qQXJHM21KZzlYSXJhbERvU3gyMW1tRApQNDBpNENtcXRPQUdIMjlYR2VNUldmdngrK1FOTmlybUJBPT0KLS0tLS1FTkQgRUMgUFJJVkFURSBLRVktLS0tLQo="
        VOLUME_TOKEN_SIGNING_KEY_NAME = "local-dev-2026-03-20"

        DEFAULT_PERSISTENT_VOLUME_TYPE = "local" # "local、vepfs"

        CLIENT_PROXY_OIDC_ISSUER_URL = ""
      }

      config {
        network_mode = "host"
        image        = "mp-bp-cn-shanghai.cr.volces.com/e2b/api:2026.29"
        ports        = ["api", "api_internal_grpc", "grpc_api"]
        args         = ["--port", "3000"]
      }
    }

    task "db-migrator" {
      driver = "docker"

      env {
        POSTGRES_CONNECTION_STRING = "postgresql://e2b:Galaxy123@192.168.162.33:5432/dev-e2b-pg?sslmode=disable"
      }

      config {
        image = "mp-bp-cn-shanghai.cr.volces.com/e2b/db-migrator:2026.29"
      }

      resources {
        cpu    = 250
        memory = 128
      }

      lifecycle {
        hook    = "prestart"
        sidecar = false
      }
    }
  }
}