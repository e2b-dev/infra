job "orchestrator-${latest_orchestrator_job_id}" {
  type = "system"
  node_pool = "${node_pool}"

  priority = 91

  group "client-orchestrator" {
    // For future as we can remove static and allow multiple instances on one machine if needed.
    // Also network allocation is used by Nomad service discovery on API and edge API to find jobs and register them.
    network {
      port "orchestrator" {
        static = "${port}"
      }

      port "orchestrator-proxy" {
        static = "${proxy_port}"
      }
    }

    constraint {
      attribute = "$${meta.orchestrator_job_version}"
      value     = "${latest_orchestrator_job_id}"
    }

    service {
      name = "orchestrator"
      port = "${port}"

      provider = "nomad"

      check {
        type         = "http"
        path         = "/health"
        name         = "health"
        interval     = "20s"
        timeout      = "5s"
      }
    }

    service {
      name = "orchestrator-proxy"
      port = "${proxy_port}"

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

      env {
        NODE_ID                      = "$${node.unique.name}"
        CONSUL_TOKEN                 = "${consul_acl_token}"
        OTEL_TRACING_PRINT           = "${otel_tracing_print}"
        LOGS_COLLECTOR_ADDRESS       = "${logs_collector_address}"
        ENVIRONMENT                  = "${environment}"
        DOMAIN_NAME                  = "${domain_name}"
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

%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
%{ endif }
      }

      config {
        command = "local/orchestrator"
      }

      artifact {
        %{ if environment == "dev" }
        // Version hash is only available for dev to increase development speed in prod use rolling updates
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/orchestrator?version=${orchestrator_checksum}"
        %{ else }
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/orchestrator"
        %{ endif }
      }
    }
  }
}
