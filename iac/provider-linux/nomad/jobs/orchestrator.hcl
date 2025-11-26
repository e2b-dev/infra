job "orchestrator" {
  type      = "system"
  node_pool = "${node_pool}"
  priority  = 90

  group "client-orchestrator" {
    service {
      name = "orchestrator"
      port = "${port}"
      provider = "nomad"
      address_mode = "host"
      check {
        type     = "http"
        path     = "/health"
        name     = "health"
        interval = "20s"
        timeout  = "5s"
      }
    }

    service {
      name = "orchestrator-proxy"
      port = "${proxy_port}"
      provider = "nomad"
      address_mode = "host"
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

      env {
        NODE_ID                      = "$${node.unique.name}"
        CONSUL_TOKEN                 = "${consul_acl_token}"
        OTEL_TRACING_PRINT           = "${otel_tracing_print}"
        LOGS_COLLECTOR_ADDRESS       = "${logs_collector_address}"
        ENVIRONMENT                  = "${environment}"
        ENVD_TIMEOUT                 = "${envd_timeout}"
        TEMPLATE_BUCKET_NAME         = "${template_bucket_name}"
        OTEL_COLLECTOR_GRPC_ENDPOINT = "${otel_collector_grpc_endpoint}"
        ALLOW_SANDBOX_INTERNET       = "${allow_sandbox_internet}"
        SHARED_CHUNK_CACHE_PATH      = "${shared_chunk_cache_path}"
        CLICKHOUSE_CONNECTION_STRING = "${clickhouse_connection_string}"
        REDIS_URL                    = "${redis_url}"
        REDIS_CLUSTER_URL            = "${redis_cluster_url}"
        REDIS_TLS_CA_BASE64          = "${redis_tls_ca_base64}"
        GRPC_PORT                    = "${port}"
        PROXY_PORT                   = "${proxy_port}"
        GIN_MODE                     = "release"
        STORAGE_PROVIDER             = "Local"
        LOCAL_TEMPLATE_STORAGE_BASE_PATH = "/tmp/templates"
        LOCAL_BUILD_CACHE_STORAGE_BASE_PATH = "/tmp/build-cache"
        ARTIFACTS_REGISTRY_PROVIDER  = "Local"
%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
%{ endif }
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " set -e; modprobe nbd nbds_max=4096 max_part=16 || true; for i in $(seq 0 4095); do if [ -e /sys/block/nbd$i/pid ]; then nbd-client -d /dev/nbd$i || true; fi; done; chmod +x local/orchestrator && exec local/orchestrator"]
      }

      artifact {
        source      = "${artifact_url}"
        destination = "local/orchestrator"
        mode        = "file"
      }
    }
  }
}