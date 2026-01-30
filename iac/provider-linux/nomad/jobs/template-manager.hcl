job "template-manager-system" {
  datacenters = ["${datacenter}"]
  node_pool   = "${node_pool}"
  type        = "system"
  priority    = 70

%{ if update_stanza }
  update {
    max_parallel = 1
  }
%{ endif }

  group "template-manager" {
    restart {
      interval = "5s"
      attempts = 1
      delay    = "5s"
      mode     = "delay"
    }

    network {
      port "template-manager" {
        static = "${port}"
      }
    }

    service {
      name     = "template-manager"
      port     = "${port}"
      address_mode = "host"
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

%{ if update_stanza }
      kill_timeout = "70m"
%{ else }
      kill_timeout = "1m"
%{ endif }
      kill_signal  = "SIGTERM"

      resources {
        memory = 1024
        cpu    = 512
      }

      env {
        NODE_ID                       = "$${node.unique.name}"
        CONSUL_TOKEN                  = "${consul_acl_token}"
        API_SECRET                    = "${api_secret}"
        OTEL_TRACING_PRINT            = "${otel_tracing_print}"
        ENVIRONMENT                   = "${environment}"
        TEMPLATE_BUCKET_NAME          = "${template_bucket_name}"
        BUILD_CACHE_BUCKET_NAME       = "${build_cache_bucket_name}"
        OTEL_COLLECTOR_GRPC_ENDPOINT  = "${otel_collector_grpc_endpoint}"
        LOGS_COLLECTOR_ADDRESS        = "${logs_collector_address}"
        ORCHESTRATOR_SERVICES         = "${orchestrator_services}"
        SHARED_CHUNK_CACHE_PATH       = "${shared_chunk_cache_path}"
        CLICKHOUSE_CONNECTION_STRING  = "${clickhouse_connection_string}"
        DOCKERHUB_REMOTE_REPOSITORY_URL  = "${dockerhub_remote_repository_url}"
        DOCKERHUB_REMOTE_REPOSITORY_PROVIDER = "${dockerhub_remote_repository_provider}"
        GRPC_PORT                     = "${port}"
        PROXY_PORT                    = "${proxy_port}"
        SANDBOX_HYPERLOOP_PROXY_PORT  = "${sandbox_hyperloop_proxy_port}"
        USE_LOCAL_NAMESPACE_STORAGE    = "${use_local_namespace_storage}"
        GIN_MODE                      = "release"
%{ if !update_stanza }
        FORCE_STOP                    = "true"
%{ endif }
%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
%{ endif }
        STORAGE_PROVIDER                  = "Local"
        LOCAL_TEMPLATE_STORAGE_BASE_PATH  = "/e2b-share/templates"
        LOCAL_BUILD_CACHE_STORAGE_BASE_PATH = "/e2b-share/build-cache"
        ARTIFACTS_REGISTRY_PROVIDER       = "Local"
        ORCHESTRATOR_BASE_PATH            = "/orchestrator"
        FIRECRACKER_VERSIONS_DIR          = "/fc-versions"
        HOST_KERNELS_DIR                  = "/fc-kernels"
%{ if should_download_envd && envd_artifact_url != "" }
        HOST_ENVD_PATH                    = "local/envd"
%{ endif }
%{ if use_nfs_share_storage }
        NFS_SERVER_IP                     = "${nfs_server_ip}"
%{ endif }
        E2B_DEBUG="true"
        FORCE_UPDATE           = "20260126-01"
      }

      template {
        data        = <<EOH
${start_script}
EOH
        destination = "local/start.sh"
        perms       = "0755"
      }

      config {
        command = "local/start.sh"
      }

      artifact {
        source      = "${artifact_url}"
        destination = "local/template-manager"
        mode        = "file"
%{ if template_manager_checksum != "" }
        options { checksum = "md5:${template_manager_checksum}" }
%{ endif }
      }

%{ if should_download_envd && envd_artifact_url != "" }
      artifact {
        source      = "${envd_artifact_url}"
        destination = "local/envd"
        mode        = "file"
      }
%{ endif }
    }
  }
}
