job "api" {
  datacenters = ["${gcp_zone}"]
  node_pool = "api"
  priority = 90

  group "api-service" {
    network {
      port "api" {
        static = "${port_number}"
      }
    }

    service {
      name = "api"
      port = "${port_number}"

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = "${port_number}"
      }
    }

    task "start" {
      driver = "docker"

      resources {
        memory_max = 4096
        memory     = 2048
        cpu        = 1024
      }

      env {
        ORCHESTRATOR_PORT             = "${orchestrator_port}"
        TEMPLATE_MANAGER_ADDRESS      = "${template_manager_address}"
        POSTGRES_CONNECTION_STRING    = "${postgres_connection_string}"
        ENVIRONMENT                   = "${environment}"
        POSTHOG_API_KEY               = "${posthog_api_key}"
        ANALYTICS_COLLECTOR_HOST      = "${analytics_collector_host}"
        ANALYTICS_COLLECTOR_API_TOKEN = "${analytics_collector_api_token}"
        LOKI_ADDRESS                  = "${loki_address}"
        OTEL_TRACING_PRINT            = "${otel_tracing_print}"
        LOGS_COLLECTOR_ADDRESS        = "${logs_collector_address}"
        NOMAD_TOKEN                   = "${nomad_acl_token}"
        OTEL_COLLECTOR_GRPC_ENDPOINT  = "${otel_collector_grpc_endpoint}"
        ADMIN_TOKEN                   = "${admin_token}"
        REDIS_URL                     = "${redis_url}"
        # This is here just because it is required in some part of our code which is transitively imported
        TEMPLATE_BUCKET_NAME          = "skip"
      }

      config {
        network_mode = "host"
        image        = "${api_docker_image}"
        ports        = ["${port_name}"]
        args = [
          "--port", "${port_number}",
        ]
      }
    }
  }
}
