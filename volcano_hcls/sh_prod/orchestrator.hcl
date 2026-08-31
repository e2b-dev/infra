job "orchestrator" {
  type        = "system"
  datacenters = ["sh-prod"]
  node_pool   = "default"
  priority    = 91

  group "client-orchestrator" {
    constraint {
      attribute = "${meta.role}"
      value     = "orchestrator"
    }

    restart {
      attempts = 3
      interval = "1m"
      delay    = "6s"
      mode     = "delay"
    }

    // Also network allocation is used by Nomad service discovery on API and edge API to find jobs and register them.
    network {
      port "orchestrator" {
        static = 9090
      }
      port "orchestrator-proxy" {
        static = 5007
      }
    }

    // orchestrator gRPC service
    // 2026.29 的 API 通过 Nomad 原生 service 注册发现 orchestrator（默认名 "orchestrator"），
    // 见 NOMAD_ORCHESTRATOR_SERVICE_NAMES。注册的 Address 必须非空，否则 API 会跳过。
    service {
      name     = "orchestrator"
      port     = "orchestrator"
      provider = "nomad"

      check {
        type     = "http"
        path     = "/health"
        name     = "health"
        interval = "20s"
        timeout  = "5s"
      }
    }

    // orchestrator proxy (sandbox traffic)
    service {
      name     = "orchestrator-proxy"
      port     = "orchestrator-proxy"
      provider = "nomad"

      check {
        type     = "tcp"
        name     = "health"
        interval = "30s"
        timeout  = "1s"
      }
    }

    task "start" {
      driver = "raw_exec"

      restart {
        attempts = 0
      }

      resources {
        memory = 81920
        cpu    = 3000
      }

      # sandbox 优雅排空：SIGTERM 后先置 Draining 等 15s，再等在运行的 sandbox 退出。
      kill_timeout = "10m"
      kill_signal  = "SIGTERM"

      env {
        NODE_ID = "${node.unique.name}"
        #NODE_ID                       = "${NOMAD_ALLOC_ID}"
        NODE_IP      = "${attr.unique.network.ip-address}"
        NODE_LABELS  = "persistent-volume-type=local"
        CONSUL_TOKEN = "5489ea72-05d7-7bf7-f886-49af103aaa5f"
        ENVIRONMENT  = "dev"

        # 仅提供 orchestrator（sandbox 运行时）服务
        ORCHESTRATOR_SERVICES = "orchestrator"

        # ⚠ 与同节点的 template-manager 分文件加锁。默认值 /orchestrator.lock 两边相同，
        # 会导致后启动的进程 log.Fatalf 退出（ENVIRONMENT=prod 时）。
        ORCHESTRATOR_LOCK_PATH = "/orchestrator.lock"

        # ⚠ 同节点跑两个 sandbox 运行时，必须关掉启动清理。
        # startupreclaim 会扫描 /proc 杀掉「所有」firecracker 进程组（无归属过滤），
        # 并清空 /run/netns 下的 ns-* 命名空间——即另一进程正在服务的 sandbox。
        DISABLE_STARTUP_RECLAIM = "true"

        # OTEL_TRACING_PRINT           = "false"
        OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:4317"
        LOGS_COLLECTOR_ADDRESS       = "http://localhost:19095"

        ENVD_TIMEOUT = ""

        TEMPLATE_BUCKET_NAME    = "prod-template"
        BUILD_CACHE_BUCKET_NAME = "e2b-build-cache"
        STORAGE_PROVIDER        = "AWSBucket"
        AWS_ENDPOINT_URL        = "https://tos-s3-cn-shanghai.ivolces.com"
        AWS_REGION              = "cn-shanghai"
        AWS_ACCESS_KEY_ID       = "REPLACE_WITH_VOLC_AK"
        AWS_SECRET_ACCESS_KEY   = "REPLACE_WITH_VOLC_SK"
        S3_USE_PATH_STYLE = "false"

        # ALLOW_SANDBOX_INTERNET 在 2026.29 代码中已无人读取，删除。
        # 允许沙箱访问的内网 CIDR（逗号分隔），为空则沙箱无法访问任何内网 IP
        ALLOW_SANDBOX_INTERNAL_CIDRS = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10"

        SHARED_CHUNK_CACHE_PATH      = ""
        CLICKHOUSE_CONNECTION_STRING = "clickhouse://default:@192.168.162.212:9000/sh-prod_e2b_clickhouse"
        # 2026.29 新增：多 ClickHouse 端点（分号分隔），留空维持单连接串。
        # CLICKHOUSE_CONNECTION_STRINGS = ""

        REDIS_URL         = "192.168.162.216:6379"
        REDIS_CLUSTER_URL = ""
        REDIS_POOL_SIZE   = "10"

        GRPC_PORT  = "9090"
        PROXY_PORT = "5007"
        # LOG_LEVEL / LAUNCH_DARKLY_ENABLE 在 2026.29 代码中均无人读取，已删除。
        # 日志级别由 E2B_DEBUG 控制；LD 未配 API key 时自动走离线数据源。
        GIN_MODE              = "release"
        LAUNCH_DARKLY_API_KEY = ""

        ORCHESTRATOR_BASE_PATH = "/data1/orchestrator"
        TMPDIR                 = "/data1/tmp"

        # ⚠ 以下 host 级监听端口在同节点上必须与 template-manager 错开，
        # 否则后启动者 bind 失败。这里保留默认值，由 template-manager 侧改。
        SANDBOX_HYPERLOOP_PROXY_PORT = "5010"
        SANDBOX_NFS_PROXY_PORT       = "5011"
        SANDBOX_PORTMAPPER_PORT      = "5012"
        # pprof 变量名是 PPROF_PORT（原文件的 PPROF 无人读取，会静默落到默认 6060）。
        PPROF_PORT = "6060"

        DOCKERHUB_REMOTE_REPOSITORY_URL = ""
        ARTIFACTS_REGISTRY_PROVIDER     = "Local"
        PERSISTENT_VOLUME_MOUNTS        = "local:/data1/orchestrator/volumes"
        DOMAIN_NAME                     = ""
      }

      config {
        command = "/opt/orchestrator/orchestrator"
      }
    }
  }
}