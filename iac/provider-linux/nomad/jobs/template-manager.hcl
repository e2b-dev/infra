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
        cpu    = 256
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
        GRPC_PORT                     = "${port}"
        SANDBOX_HYPERLOOP_PROXY_PORT  = "${sandbox_hyperloop_proxy_port}"
        GIN_MODE                      = "release"
%{ if !update_stanza }
        FORCE_STOP                    = "true"
%{ endif }
%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
%{ endif }
        STORAGE_PROVIDER                  = "Local"
        LOCAL_TEMPLATE_STORAGE_BASE_PATH  = "/tmp/templates"
        LOCAL_BUILD_CACHE_STORAGE_BASE_PATH = "/tmp/build-cache"
        ARTIFACTS_REGISTRY_PROVIDER       = "Local"
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " set -e; modprobe nbd nbds_max=4096 max_part=16 || true; for i in $(seq 0 4095); do if [ -e /sys/block/nbd$i/pid ]; then nbd-client -d /dev/nbd$i || true; fi; done; chmod +x local/template-manager && exec local/template-manager"]
      }

      artifact {
        source      = "${artifact_url}"
        destination = "local/template-manager"
        mode        = "file"
%{ if template_manager_checksum != "" }
        options { checksum = "md5:${template_manager_checksum}" }
%{ endif }
      }
    }
  }
}