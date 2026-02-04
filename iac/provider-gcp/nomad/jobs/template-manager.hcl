job "template-manager" {
  type = "service"
  node_pool  = "${node_pool}"
  priority = 75

  group "template-manager" {
    # Count is fetched from current Nomad state to preserve autoscaler-managed value
    count = ${current_count}

    # Ensure one allocation per node (like a system job)
    constraint {
      operator = "distinct_hosts"
      value    = "true"
    }

%{ if update_stanza }
    # Scaling policy to match node count in the pool
    # Uses the nomad-nodepool APM plugin
    scaling {
      enabled = true
      min     = 2
      max     = 10000  # Effectively unlimited

      policy {
        evaluation_interval = "10s"
        cooldown            = "2m"

        check "match_node_count" {
          source = "nomad-nodepool-apm"
          query  = "${node_pool}"

          strategy "pass-through" {}
        }
      }
    }

    # Rolling update configuration for service jobs
    # https://developer.hashicorp.com/nomad/docs/job-specification/update
    update {
      max_parallel      = 1
      min_healthy_time  = "10s"
      healthy_deadline  = "2m"
      progress_deadline = "80m"  # Must be > healthy_deadline and > kill_timeout
      auto_revert       = false
    }
%{ endif }

    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    // For future as we can remove static and allow multiple instances on one machine if needed.
    // Also network allocation is used by Nomad service discovery on API and edge API to find jobs and register them.
    network {
      port "template-manager" {
        static = "${port}"
      }
    }

    service {
      name     = "template-manager"
      port     = "${port}"
      provider = "nomad"

      check {
        type         = "http"
        path         = "/health"
        name         = "health"
        interval     = "20s"
        timeout      = "5s"
      }
    }

    task "start" {
      driver = "raw_exec"

%{ if update_stanza }
      # https://developer.hashicorp.com/nomad/docs/configuration/client#max_kill_timeout
      kill_timeout      = "70m"
%{ else }
      kill_timeout      = "1m"
%{ endif }
      kill_signal  = "SIGTERM"

      resources {
        memory     = 1024
        cpu        = 256
      }

      env {
        NODE_ID                       = "$${node.unique.name}"
        CONSUL_TOKEN                  = "${consul_acl_token}"
        GOOGLE_SERVICE_ACCOUNT_BASE64 = "${google_service_account_key}"
        GCP_PROJECT_ID                = "${gcp_project}"
        GCP_REGION                    = "${gcp_region}"
        GCP_DOCKER_REPOSITORY_NAME    = "${docker_registry}"
        API_SECRET                    = "${api_secret}"
        OTEL_TRACING_PRINT            = "${otel_tracing_print}"
        ENVIRONMENT                   = "${environment}"
        DOMAIN_NAME                   = "${domain_name}"
        TEMPLATE_BUCKET_NAME          = "${template_bucket_name}"
        BUILD_CACHE_BUCKET_NAME       = "${build_cache_bucket_name}"
        OTEL_COLLECTOR_GRPC_ENDPOINT  = "${otel_collector_grpc_endpoint}"
        LOGS_COLLECTOR_ADDRESS        = "${logs_collector_address}"
        ORCHESTRATOR_SERVICES         = "${orchestrator_services}"
        SHARED_CHUNK_CACHE_PATH       = "${shared_chunk_cache_path}"
        CLICKHOUSE_CONNECTION_STRING  = "${clickhouse_connection_string}"
        DOCKERHUB_REMOTE_REPOSITORY_URL  = "${dockerhub_remote_repository_url}"
        GRPC_PORT                     = "${port}"
        GIN_MODE                      = "release"
%{ if !update_stanza }
        FORCE_STOP                    = "true"
%{ endif }
%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
%{ endif }
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " chmod +x local/template-manager && local/template-manager"]
      }

      artifact {
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/template-manager"
        options {
            checksum    = "md5:${template_manager_checksum}"
        }
      }
    }
  }
}
