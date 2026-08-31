job "template-manager-1" {
  type      = "system"
  node_pool = "default"
  priority  = 91

  group "template-manager" {
    constraint {
      attribute = "${meta.role}"
      #attribute = "${meta.templaterole}"
      value     = "orchestrator1"
      #value     = "template-manager"
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
      port     = "orchestrator"
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

      # restart {
      #   attempts = 0
      # }

      resources {
        memory = 20480
        cpu    = 3000
      }

      kill_timeout = "10m"
      kill_signal  = "SIGTERM"

      env {
        NODE_ID = "${node.unique.name}"
        NODE_IP = "${attr.unique.network.ip-address}"

        CONSUL_TOKEN = "c2cc0f33-cf29-2571-ceab-1b965ce6d0e8"
        ENVIRONMENT  = "dev"

        # 合并部署：orchestrator 二进制同时提供两个服务
        ORCHESTRATOR_SERVICES = "orchestrator,template-manager"

        OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:4317"
        LOGS_COLLECTOR_ADDRESS       = "http://localhost:19095"

        ENVD_TIMEOUT = ""

        TEMPLATE_BUCKET_NAME    = "dev-template"
        BUILD_CACHE_BUCKET_NAME = "e2b-build-cache"
        STORAGE_PROVIDER        = "AWSBucket"
        AWS_ENDPOINT_URL        = "https://tos-s3-cn-shanghai.ivolces.com"
        AWS_REGION              = "cn-shanghai"
        AWS_ACCESS_KEY_ID       = "REPLACE_WITH_VOLC_AK"
        AWS_SECRET_ACCESS_KEY   = "REPLACE_WITH_VOLC_SK"
        S3_USE_PATH_STYLE = "false"

        ARTIFACTS_REGISTRY_PROVIDER = "Local"

        ALLOW_SANDBOX_INTERNET       = "true"
        ALLOW_SANDBOX_INTERNAL_CIDRS = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10"

        SHARED_CHUNK_CACHE_PATH = ""
        #CLICKHOUSE_CONNECTION_STRING = ""
        CLICKHOUSE_CONNECTION_STRING = "clickhouse://default:@192.168.162.30:9000/dev_e2b_clickhouse"

        REDIS_URL           = "192.168.162.32:6379"
        REDIS_CLUSTER_URL   = ""
        REDIS_POOL_SIZE     = "10"
        REDIS_TLS_CA_BASE64 = ""

        GRPC_PORT  = "9090"
        PROXY_PORT = "5007"
        GIN_MODE   = "release"
        LOG_LEVEL  = "debug"

        LAUNCH_DARKLY_API_KEY = ""

        SANDBOX_HYPERLOOP_PROXY_PORT = "5010"
        PPROF                        = "6060"

        DOCKERHUB_REMOTE_REPOSITORY_URL = ""

        # PERSISTENT_VOLUME_MOUNTS = "local:/data/e2b-sbx-share"
        PERSISTENT_VOLUME_MOUNTS = "local:/data/e2b-sbx-share,vepfs:/mnt/volcano-vepfs/volumes" # /路径有问题卡死，不能改成/volcano-vepfs/volumes，需要排查

        DOMAIN_NAME = ""
      }

      config {
        command = "/opt/orchestrator/orchestrator"
      }
    }
  }
}