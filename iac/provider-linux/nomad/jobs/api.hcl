job "api" {
  datacenters = ["${datacenter}"]
  node_pool   = "${node_pool}"
  priority = 90

  group "api-service" {
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "api" {
        static = "${port_number}"
      }

      %{ if prevent_colocation }
      port "scheduling-block" {
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
      port = "${port_number}"
      task = "start"
      tags = [
        "traefik.enable=true",
        "traefik.http.routers.api.rule=Host(`api.${domain_name}`)",
        "traefik.http.routers.api.entrypoints=websecure",
        "traefik.http.routers.api.tls=true",
        "traefik.http.routers.api.service=api",
        "traefik.http.services.api.loadbalancer.server.port=${port_number}"
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "${port_number}"
      }
    }

%{ if update_stanza }
    update {
      max_parallel     = 1
      canary           = 1
      min_healthy_time = "10s"
      healthy_deadline = "300s"
      auto_promote     = true
    }
%{ endif }

    task "start" {
      driver       = "docker"
      kill_timeout = "30s"
      kill_signal  = "SIGTERM"

      resources {
        memory_max = ${memory_mb * 2}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        NODE_ID                        = "$${node.unique.id}"
        ORCHESTRATOR_PORT              = "${orchestrator_port}"
        TEMPLATE_MANAGER_HOST          = "${template_manager_host}"
        POSTGRES_CONNECTION_STRING     = "${postgres_connection_string}"
        SUPABASE_JWT_SECRETS           = "${supabase_jwt_secrets}"
        CLICKHOUSE_CONNECTION_STRING   = "${clickhouse_connection_string}"
        ENVIRONMENT                    = "${environment}"
        POSTHOG_API_KEY                = "${posthog_api_key}"
        ANALYTICS_COLLECTOR_HOST       = "${analytics_collector_host}"
        ANALYTICS_COLLECTOR_API_TOKEN  = "${analytics_collector_api_token}"
        OTEL_TRACING_PRINT             = "${otel_tracing_print}"
        LOGS_COLLECTOR_ADDRESS         = "${logs_collector_address}"
        NOMAD_TOKEN                    = "${nomad_acl_token}"
        OTEL_COLLECTOR_GRPC_ENDPOINT   = "${otel_collector_grpc_endpoint}"
        ADMIN_TOKEN                    = "${admin_token}"
        REDIS_URL                      = "${redis_url}"
        REDIS_CLUSTER_URL              = "${redis_secure_cluster_url}"
        DNS_PORT                       = "${dns_port_number}"
        SANDBOX_ACCESS_TOKEN_HASH_SEED = "${sandbox_access_token_hash_seed}"
        
        LOCAL_CLUSTER_ENDPOINT = "${local_cluster_endpoint}"
        LOCAL_CLUSTER_TOKEN    = "${local_cluster_token}"

%{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
%{ endif }

        TEMPLATE_BUCKET_NAME          = "skip"
      }

      config {
        network_mode = "host"
        image        = "${api_docker_image}"
        ports        = ["${port_name}"]
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