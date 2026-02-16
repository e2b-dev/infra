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

%{ if latest_orchestrator_job_id != "dev" }
    constraint {
      attribute = "$${meta.orchestrator_job_version}"
      value     = "${latest_orchestrator_job_id}"
    }
%{ endif }

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
        LOGS_COLLECTOR_ADDRESS       = "${logs_collector_address}"
        ENVIRONMENT                  = "${environment}"
        ENVD_TIMEOUT                 = "${envd_timeout}"
        TEMPLATE_BUCKET_NAME         = "${template_bucket_name}"
        OTEL_COLLECTOR_GRPC_ENDPOINT = "${otel_collector_grpc_endpoint}"
        ALLOW_SANDBOX_INTERNET       = "${allow_sandbox_internet}"
        CLICKHOUSE_CONNECTION_STRING = "${clickhouse_connection_string}"
        REDIS_URL                    = "${redis_url}"
        REDIS_CLUSTER_URL            = "${redis_cluster_url}"
        REDIS_TLS_CA_BASE64          = "${redis_tls_ca_base64}"
        GRPC_PORT                    = "${port}"
        PROXY_PORT                   = "${proxy_port}"
        GIN_MODE                     = "release"

        CONSUL_TOKEN                 = "${consul_token}"
        DOMAIN_NAME                  = "${domain_name}"
        SHARED_CHUNK_CACHE_PATH      = "${shared_chunk_cache_path}"
        ORCHESTRATOR_SERVICES        = "${orchestrator_services}"

%{ if build_cache_bucket_name != "" }
        BUILD_CACHE_BUCKET_NAME      = "${build_cache_bucket_name}"
%{ endif }

%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY        = "${launch_darkly_api_key}"
%{ endif }

%{ if use_local_namespace_storage }
        USE_LOCAL_NAMESPACE_STORAGE  = "true"
%{ endif }

%{ if provider == "gcp" }
        ARTIFACTS_REGISTRY_PROVIDER  = "GCP_ARTIFACTS"
        STORAGE_PROVIDER             = "GCPBucket"
%{ endif }
%{ if provider == "aws" }
        ARTIFACTS_REGISTRY_PROVIDER  = "AWS_ECR"
        STORAGE_PROVIDER             = "AWSBucket"

        AWS_REGION                   = "${provider_aws_config.region}"
        AWS_DOCKER_REPOSITORY_NAME   = "${provider_aws_config.docker_repository_name}"
%{ endif }
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " chmod +x local/orchestrator && local/orchestrator"]
      }

      artifact {
        source = "${artifact_source}"
      }
    }
  }
}
