job "api" {
  type        = "service"
  datacenters = ["sh-prod"]
  node_pool   = "api"
  priority    = 90

  group "api-service" {
    count = 1

    constraint {
      attribute = "${meta.role}"
      value     = "api"
      # attribute = "${node.unique.id}"
      # value     = "dda6df4b-b17f-817c-8e8c-07640c045be3"
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
      canary = 1
      # Time the canary must stay healthy before it is promoted and the old
      # allocation is stopped. 上游 2026.29 把这里提到 120s，是为了填补 GCP LB
      # 把新建 MIG 节点纳为可路由后端的 ~60s 空窗；本 job 是 count=1 且用
      # node.unique.id 钉死在固定节点、经 traefik + Consul 路由，不存在该空窗，
      # 故保持 10s。
      min_healthy_time = "10s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline = "10800s"
      # Time to wait for the overall update to complete. Otherwise, the deployment is marked as failed and rolled back
      # This is on purpose very tight, we want to fail immediately if the deployment is marked as unhealthy
      progress_deadline = "10801s"
      # Whether to promote the canary if the rest of the group is not healthy
      auto_promote = true
      # Whether to automatically rollback if the update fails
      auto_revert = true
    }

    task "start" {
      driver = "docker"
      # Budget = shutdownDrainWait (15s) + shutdownTimeout (requestTimeout 70s + 5s) + cleanup (30s) + slack.
      kill_timeout = "150s"
      kill_signal  = "SIGTERM"

      resources {
        memory_max = 10240
        memory     = 8192
        cpu        = 2000
      }

      env {
        ENVIRONMENT                    = "dev"
        DOMAIN_NAME                    = "${domain_name}"
        NODE_ID                        = "${node.unique.id}"
        NOMAD_TOKEN                    = "787cd88e-3d5a-d6c6-5544-be2b08bc3f5f"
        E2B_DEBUG                      = "true"
        ORCHESTRATOR_PORT              = "9090"
        API_INTERNAL_GRPC_PORT         = "5009"
        API_EDGE_GRPC_PORT             = "${NOMAD_PORT_grpc_api}"
        ADMIN_TOKEN                    = "dev-admin-token-change-in-production"
        SANDBOX_ACCESS_TOKEN_HASH_SEED = "dev-random-seed-change-in-production"

        # 2026.29：orchestrator 发现改为读取 Nomad 原生 service 注册（默认名
        # "orchestrator"），orchestrator-with-template.hcl 已注册该 service，
        # 保持默认值即可。同时保留基于 default 节点池的旧发现方式作为兜底
        # （默认 true），两者取并集，无需按顺序滚动升级。
        # NOMAD_ORCHESTRATOR_SERVICE_NAMES            = "orchestrator"
        # NOMAD_ORCHESTRATOR_LEGACY_DISCOVERY_ENABLED = "true"

        POSTGRES_CONNECTION_STRING = "postgresql://e2b:Galaxy123@192.168.162.24:5432/sh-prod?sslmode=disable"
        #AUTH_PROVIDER_CONFIG       = "{\"jwt\":[{\"issuer\":{\"url\":\"https://auth.xiaobei.top\",\"audiences\":[\"e2b-sh-prod\"]},\"cacheDuration\":\"5m\"}]}"
        #AUTH_PROVIDER_CONFIG       = "{\"jwt\":[{\"issuer\":{\"url\":\"https://auth.xiaobei.top\",\"audiences\":[\"e2b-sh-prod\"]},\"cacheDuration\":\"5m\"}],"legacy":{"hmac":{"secrets":["xiaobei-dashboard-jwt-secret-2026-dev"]}}}"
        # ⚠ 2026.29 已删除 legacy HMAC 鉴权（packages/auth/pkg/auth/legacy/ 整包移除，
        # ProviderConfig 现在只剩 jwt 字段）。下面的 "legacy" 键不会报错——
        # json.Unmarshal 默认忽略未知字段——而是被静默丢弃，靠该 HMAC secret
        # 签发 token 的调用方会在升级后直接 401。升级前需先把 dashboard 等
        # 调用方切到 OIDC issuer 签发的 token。
        AUTH_PROVIDER_CONFIG = "{\"jwt\":[{\"issuer\":{\"url\":\"https://auth.xiaobei.top\",\"audiences\":[\"e2b-sh-prod\"]},\"cacheDuration\":\"5m\"}],\"legacy\":{\"hmac\":{\"secrets\":[\"xiaobei-dashboard-jwt-secret-2026-dev\"]}}}"

        CLICKHOUSE_CONNECTION_STRING = "clickhouse://default:@192.168.162.212:9000/sh-prod_e2b_clickhouse"
        # 2026.29 新增：多 ClickHouse 集群按 LD 开关轮换读取（分号分隔）。
        # 留空即维持单连接串行为。
        # CLICKHOUSE_CONNECTION_STRINGS = ""

        REDIS_URL         = "192.168.162.216:6379"
        REDIS_CLUSTER_URL = ""

        POSTHOG_API_KEY               = ""
        ANALYTICS_COLLECTOR_HOST      = ""
        ANALYTICS_COLLECTOR_API_TOKEN = ""
        LOGS_COLLECTOR_ADDRESS        = "http://localhost:19095"
        OTEL_COLLECTOR_GRPC_ENDPOINT  = "localhost:4317"
        # OTEL_TRACING_PRINT            = "false"
        LOKI_URL = "http://localhost:3100"

        LAUNCH_DARKLY_API_KEY = ""
        # LAUNCH_DARKLY_ENABLE  = "false"

        # DNS_PORT                           = "53"
        # LOCAL_CLUSTER_ENDPOINT             = "${attr.unique.network.ip-address}:3001"
        # LOCAL_CLUSTER_TOKEN                = "local-token"
        # LOCAL_CLUSTER_SANDBOX_PROXY_DOMAIN = "hk-prod-e2b.xiaobei.top"


        TEMPLATE_BUCKET_NAME  = "prod-template"
        STORAGE_PROVIDER      = "AWSBucket"
        AWS_ENDPOINT_URL      = "https://tos-s3-cn-shanghai.ivolces.com"
        AWS_REGION            = "cn-shanghai"
        AWS_ACCESS_KEY_ID     = "REPLACE_WITH_VOLC_AK"
        AWS_SECRET_ACCESS_KEY = "REPLACE_WITH_VOLC_SK"
        S3_USE_PATH_STYLE = "false"


        # 2026.29：签名开关显式化。VOLUME_TOKEN_ENABLED 默认 true，此时下面四项
        # 缺一不可，否则进程启动即报错退出；若不启用 volume，可置 false 并删除下面四项。
        VOLUME_TOKEN_ENABLED          = "true"
        VOLUME_TOKEN_ISSUER           = "local.e2b.dev"
        VOLUME_TOKEN_SIGNING_METHOD   = "ES256"
        VOLUME_TOKEN_SIGNING_KEY      = "ECDSA:LS0tLS1CRUdJTiBFQyBQUklWQVRFIEtFWS0tLS0tCk1IY0NBUUVFSUFna0FCZ000a0lIa0VPVWdTNTVZeldVTjRkV3k0WjY4R2c2TUpUTGFabkRvQW9HQ0NxR1NNNDkKQXdFSG9VUURRZ0FFbFFnQ3RnWnkrb3RoUDA5bk4yUWdVNjB6ekxNaW9qQXJHM21KZzlYSXJhbERvU3gyMW1tRApQNDBpNENtcXRPQUdIMjlYR2VNUldmdngrK1FOTmlybUJBPT0KLS0tLS1FTkQgRUMgUFJJVkFURSBLRVktLS0tLQo="
        VOLUME_TOKEN_SIGNING_KEY_NAME = "local-dev-2026-03-20"

        DEFAULT_PERSISTENT_VOLUME_TYPE = "local"

        # 2026.29 新增：edge gRPC（grpc_api 端口）改为要求 OIDC Bearer 鉴权。
        # 留空时 API 仅打印告警，并拒绝所有 edge gRPC 请求；集群内 client-proxy
        # 走的是 api-internal-grpc（5009），不受影响。若要启用外部 edge 调用，
        # 填入签发方 issuer URL，且 token 需带 scope "sandboxes:lifecycle"。
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
        POSTGRES_CONNECTION_STRING = "postgresql://e2b:Galaxy123@192.168.162.24:5432/sh-prod?sslmode=disable"
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