job "template-manager" {
  type        = "system"
  datacenters = ["sh-prod"]
  node_pool   = "default"
  priority    = 75

  group "template-manager" {
    constraint {
      attribute = "${meta.role}"
      value     = "orchestrator"
    }

    // Try to restart the task indefinitely
    restart {
      interval = "5s"
      attempts = 1
      delay    = "5s"
      mode     = "delay"
    }

    network {
      port "template-manager" {
        static = 5008
      }
    }

    // template-manager gRPC service
    // 拆分后 /health 由本进程自己的 GRPC_PORT(5008) 提供，不再借用 orchestrator 的端口。
    service {
      name     = "template-manager"
      port     = "template-manager"
      provider = "nomad"

      check {
        type     = "http"
        path     = "/health"
        name     = "health"
        interval = "20s"
        timeout  = "5s"
      }
    }

    task "start" {
      driver = "raw_exec"

      restart {
        attempts = 0
      }

      resources {
        memory = 32768
        cpu    = 2000
      }

      # 模板构建可能长时间运行，留足排空时间。
      kill_timeout = "70m"
      kill_signal  = "SIGTERM"

      env {
        NODE_ID      = "${node.unique.name}"
        NODE_IP      = "${attr.unique.network.ip-address}"
        NODE_LABELS  = "${meta.node_labels}"
        CONSUL_TOKEN = "5489ea72-05d7-7bf7-f886-49af103aaa5f"
        ENVIRONMENT  = "dev"

        # 仅提供 template-manager 服务
        ORCHESTRATOR_SERVICES = "template-manager"

        # ⚠ 与同节点的 orchestrator 分文件加锁，避免二者互相抢 /orchestrator.lock。
        ORCHESTRATOR_LOCK_PATH = "/template-manager.lock"

        # ⚠ 同节点跑两个 sandbox 运行时，必须关掉启动清理，
        # 否则本进程启动会杀光 orchestrator 正在服务的 firecracker 与网络命名空间。
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

        SHARED_CHUNK_CACHE_PATH      = "/data1/SharedChunkCacheDir"
        CLICKHOUSE_CONNECTION_STRING = "clickhouse://default:@192.168.162.212:9000/sh-prod_e2b_clickhouse"

        REDIS_URL         = "192.168.162.216:6379"
        REDIS_CLUSTER_URL = ""
        REDIS_POOL_SIZE   = "10"

        GRPC_PORT  = "5008"
        PROXY_PORT = "5009"
        # LOG_LEVEL / LAUNCH_DARKLY_ENABLE 在 2026.29 代码中均无人读取，已删除。
        GIN_MODE              = "release"
        LAUNCH_DARKLY_API_KEY = ""

        # ⚠ 必须与 orchestrator 用不同的 base path：
        # SANDBOX_CACHE_DIR / TEMPLATE_CACHE_DIR 默认从 ORCHESTRATOR_BASE_PATH 派生，
        # 两个进程共用同一目录会互相覆盖 sandbox 缓存文件。
        ORCHESTRATOR_BASE_PATH = "/data1/template-manager"
        TMPDIR                 = "/data1/tmp-tm"

        # ⚠ host 级监听端口全部与 orchestrator 错开（默认 5010/5011/5012、pprof 6060）。
        SANDBOX_HYPERLOOP_PROXY_PORT = "5020"
        SANDBOX_NFS_PROXY_PORT       = "5021"
        SANDBOX_PORTMAPPER_PORT      = "5022"
        # TCP firewall proxy 默认 5016/5017/5018，与 orchestrator 撞端口，需错开。
        SANDBOX_TCP_FIREWALL_HTTP_PORT  = "5026"
        SANDBOX_TCP_FIREWALL_TLS_PORT   = "5027"
        SANDBOX_TCP_FIREWALL_OTHER_PORT = "5028"
        # pprof 变量名是 PPROF_PORT；不设会落到默认 6060，与 orchestrator 撞端口。
        PPROF_PORT = "6061"

        DOCKERHUB_REMOTE_REPOSITORY_URL = ""
        ARTIFACTS_REGISTRY_PROVIDER     = "Local"
        PERSISTENT_VOLUME_MOUNTS        = "local:/data1/template-manager/volumes"
        DOMAIN_NAME                     = ""
      }

      config {
        command = "/opt/orchestrator/orchestrator"
      }
    }
  }
}