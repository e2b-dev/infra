job "api" {
  node_pool = "${node_pool}"
  priority = 90

  group "api-service" {
    count = ${count}

    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      %{ if consul_connect_enabled }
      mode = "bridge"

      %{ endif }
      port "api" {
        static = "${port_number}"
        %{ if consul_connect_enabled }
        to     = "${port_number}"
        %{ endif }
      }

      port "api_internal_grpc" {
        static = "${api_internal_grpc_port}"
        %{ if consul_connect_enabled }
        to     = "${api_internal_grpc_port}"
        %{ endif }
      }

      port "grpc_api" {}

      %{ if prevent_colocation }
      port "scheduling-block" {
        // This port is used to block scheduling of jobs with the same block on the same node.
        // We use this to block API and Loki from being scheduled on the same node.
        static = 40234
      }
      %{ endif }
    }

    constraint {
      operator  = "distinct_hosts"
      value     = "true"
    }

    service {
      name = "api"
      port = "${port_name}"
      task = "start"
      address_mode = "host"

      tags = [
        "traefik.enable=true",

        "traefik.http.routers.api.rule=HostRegexp(`api.{domain:.+}`)",
        "traefik.http.routers.api.entrypoints=web",
        "traefik.http.routers.api.ruleSyntax=v2",
        "traefik.http.routers.api.priority=500"
      ]

      check {
        address_mode = "host"
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "${port_name}"
      }
    }

    service {
      name = "api-internal-grpc"
      port = "api_internal_grpc"
      task = "start"

      %{ if consul_connect_enabled }
      connect {
        sidecar_service {
          proxy {
            local_service_address = "127.0.0.1"
            local_service_port    = ${api_internal_grpc_port}
          }
        }
      }

      %{ endif }
      %{ if !consul_connect_enabled }
      check {
        type     = "tcp"
        name     = "api-internal-grpc"
        interval = "3s"
        timeout  = "3s"
        port     = "api_internal_grpc"
      }
      %{ endif }
    }

    service {
      name = "grpc-api"
      port = "grpc_api"
      task = "start"
      address_mode = "host"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.grpc-api-web.rule=HostRegexp(`grpc-api.{domain:.+}`)",
        "traefik.http.routers.grpc-api-web.entrypoints=web",
        "traefik.http.routers.grpc-api-web.ruleSyntax=v2",
        "traefik.http.routers.grpc-api-web.priority=500",
        "traefik.http.routers.grpc-api-web.service=grpc-api",
        %{ if grpc_api_http2_tls_enabled }
        "traefik.http.routers.grpc-api-websecure.rule=Host(`grpc-api.${domain_name}`)",
        "traefik.http.routers.grpc-api-websecure.entrypoints=websecure",
        "traefik.http.routers.grpc-api-websecure.priority=600",
        "traefik.http.routers.grpc-api-websecure.service=grpc-api",
        "traefik.http.routers.grpc-api-websecure.tls=true",
        %{ if grpc_api_http2_mtls_enabled }
        "traefik.http.routers.grpc-api-websecure.tls.options=gcp-lb-mtls@file",
        %{ endif }
        %{ endif }
        "traefik.http.services.grpc-api.loadbalancer.server.scheme=h2c"
      ]

      check {
        address_mode = "host"
        type     = "tcp"
        name     = "grpc-api"
        interval = "3s"
        timeout  = "3s"
        port     = "grpc_api"
      }
    }

%{ if update_stanza }
    # An update stanza to enable rolling updates of the service
    update {
      # The number of extra instances to run during the update
      max_parallel      = 1
      # Allows to spawn new version of the service before killing the old one
      canary            = 1
      # Time to wait for the canary to be healthy
      min_healthy_time  = "10s"
      # Time to wait for the canary to be healthy, if not it will be marked as failed
      healthy_deadline  = "10800s"
      # Time to wait for the overall update to complete. Otherwise, the deployment is marked as failed and rolled back
      # This is on purpose very tight, we want to fail immediately if the deployment is marked as unhealthy
      progress_deadline = "10801s"
      # Whether to promote the canary if the rest of the group is not healthy
      auto_promote      = true
      # Whether to automatically rollback if the update fails
      auto_revert       = true
    }
%{ endif }

    task "start" {
      driver       = "docker"
      # If we need more than 30s we will need to update the max_kill_timeout in nomad
      # https://developer.hashicorp.com/nomad/docs/configuration/client#max_kill_timeout
      kill_timeout = "30s"
      kill_signal  = "SIGTERM"

      resources {
        memory_max = ${memory_mb * 2}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        ENVIRONMENT                    = "${environment}"
        DOMAIN_NAME                    = "${domain_name}"
        NODE_ID                        = "$${node.unique.id}"
        NOMAD_TOKEN                    = "${nomad_acl_token}"
        ORCHESTRATOR_PORT              = "${orchestrator_port}"
        NOMAD_ADDRESS                  = "${nomad_address}"
        API_INTERNAL_GRPC_PORT         = "${api_internal_grpc_port}"
        API_EDGE_GRPC_PORT             = "$${NOMAD_PORT_grpc_api}"
        ADMIN_TOKEN                    = "${admin_token}"
        SANDBOX_ACCESS_TOKEN_HASH_SEED = "${sandbox_access_token_hash_seed}"

        POSTGRES_CONNECTION_STRING              = "${postgres_connection_string}"
        DB_MAX_OPEN_CONNECTIONS                = "${db_max_open_connections}"
        DB_MIN_IDLE_CONNECTIONS                = "${db_min_idle_connections}"
        AUTH_DB_CONNECTION_STRING               = "${postgres_connection_string}"
        AUTH_DB_READ_REPLICA_CONNECTION_STRING  = "${postgres_read_replica_connection_string}"
        AUTH_DB_MAX_OPEN_CONNECTIONS           = "${auth_db_max_open_connections}"
        AUTH_DB_MIN_IDLE_CONNECTIONS           = "${auth_db_min_idle_connections}"
        SUPABASE_JWT_SECRETS                    = "${supabase_jwt_secrets}"

        LOKI_URL                      = "${loki_url}"
        CLICKHOUSE_CONNECTION_STRING  = "${clickhouse_connection_string}"

        POSTHOG_API_KEY                = "${posthog_api_key}"
        ANALYTICS_COLLECTOR_HOST       = "${analytics_collector_host}"
        ANALYTICS_COLLECTOR_API_TOKEN  = "${analytics_collector_api_token}"
        LOGS_COLLECTOR_ADDRESS         = "${logs_collector_address}"
        OTEL_COLLECTOR_GRPC_ENDPOINT   = "${otel_collector_grpc_endpoint}"

        REDIS_POOL_SIZE                = "${redis_pool_size}"
        REDIS_CLUSTER_URL              = "${redis_cluster_url}"
        REDIS_TLS_CA_BASE64            = "${redis_tls_ca_base64}"
        REDIS_URL                      = "${redis_url}"

        SANDBOX_STORAGE_BACKEND        = "${sandbox_storage_backend}"

%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
%{ endif }

        # This is here just because it is required in some part of our code which is transitively imported
        TEMPLATE_BUCKET_NAME          = "skip"

%{ if default_persistent_volume_type != "" }
        DEFAULT_PERSISTENT_VOLUME_TYPE = "${ default_persistent_volume_type }"
%{ endif }

%{ for key, value in job_env_vars }
  %{ if value != "" }
        ${ key } = "${ value }"
  %{ endif }
%{ endfor }
      }

      config {
        %{ if !consul_connect_enabled }
        network_mode = "host"
        %{ endif }
        image        = "${api_docker_image}"
        ports        = ["${port_name}", "api_internal_grpc", "grpc_api"]
        args         = [
          "--port", "${port_number}",
        ]
      }
    }

    task "db-migrator" {
      driver = "docker"

      env {
        POSTGRES_CONNECTION_STRING="${postgres_connection_string}"
      }

      config {
        image = "${db_migrator_docker_image}"
      }

      resources {
        cpu    = 250
        memory = 128
      }

      lifecycle {
        hook = "prestart"
        sidecar = false
      }
    }
  }
}
