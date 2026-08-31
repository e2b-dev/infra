job "template-manager" {
  type      = "system"
  node_pool = "default"
  priority  = 91

  group "template-manager" {
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

    // For future as we can remove static and allow multiple instances on one machine if needed.
    // Also network allocation is used by Nomad service discovery on API and edge API to find jobs and register them.
    network {
      port "orchestrator" {
        static = 9090
      }
      port "orchestrator-proxy" {
        static = 5007
      }
      port "template-manager" {
        static = 5008
      }
    }

    // orchestrator gRPC service
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

    // template-manager gRPC service (合并部署，health check 复用 orchestrator 的 /health 端点)
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
        port     = "orchestrator" # 合并部署时 /health 只在 GRPC_PORT(9090) 上
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

      kill_timeout = "10m"
      kill_signal  = "SIGTERM"

      env {
        NODE_ID = "${node.unique.name}"
        NODE_IP = "${attr.unique.network.ip-address}"

        CONSUL_TOKEN = "f96c0048-a968-6690-0d71-a4bd84a7fc3f"
        ENVIRONMENT  = "dev"

        # 合并部署：orchestrator 二进制同时提供两个服务
        ORCHESTRATOR_SERVICES = "orchestrator,template-manager"

        OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:4317"
        LOGS_COLLECTOR_ADDRESS       = "http://localhost:19095"

        ENVD_TIMEOUT = ""

        TEMPLATE_BUCKET_NAME    = "yd-pde-e2b"
        BUILD_CACHE_BUCKET_NAME = "yd-pde-e2b-cache"
        STORAGE_PROVIDER        = "AWSBucket"
        AWS_ENDPOINT_URL        = "http://10.10.163.22:9000" # MinIO 内网 endpoint；
        AWS_REGION              = "us-east-1"                # MinIO 忽略，SDK 需有值
        AWS_ACCESS_KEY_ID       = "REPLACE_WITH_VOLC_AK"
        AWS_SECRET_ACCESS_KEY   = "REPLACE_WITH_VOLC_SK"
        AWS_S3_FORCE_PATH_STYLE = "true"

        ARTIFACTS_REGISTRY_PROVIDER = "Local"

        ALLOW_SANDBOX_INTERNET       = "true"
        ALLOW_SANDBOX_INTERNAL_CIDRS = ""

        SHARED_CHUNK_CACHE_PATH      = ""
        CLICKHOUSE_CONNECTION_STRING = "clickhouse://default:@10.10.163.22:9002/prod_e2b_clickhouse"

        REDIS_URL         = "10.10.163.22:6379"
        REDIS_CLUSTER_URL = ""
        # REDIS_POOL_SIZE     = "10"
        # REDIS_TLS_CA_BASE64 = ""

        GRPC_PORT  = "9090"
        PROXY_PORT = "5007"
        GIN_MODE   = "release"
        LOG_LEVEL  = "debug"

        LAUNCH_DARKLY_API_KEY  = ""
        ORCHESTRATOR_BASE_PATH = "/data1/orchestrator"
        TMPDIR                 = "/data1/tmp"

        SANDBOX_HYPERLOOP_PROXY_PORT = "5010"
        PPROF                        = "6060"

        DOCKERHUB_REMOTE_REPOSITORY_URL = ""

        PERSISTENT_VOLUME_MOUNTS = "local:/data1/orchestrator/volumes"

        DOMAIN_NAME = ""
      }

      config {
        command = "/opt/orchestrator/orchestrator"
      }
    }
  }
}
