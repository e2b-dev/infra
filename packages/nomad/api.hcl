variable "gcp_zone" {
  type    = string
  default = "us-central1-a"
}

variable "image_name" {
  type    = string
  default = ""
}

variable "api_port_name" {
  type    = string
  default = ""
}

variable "admin_token" {
  type    = string
  default = ""
}

variable "api_port_number" {
  type    = number
  default = 0
}

variable "postgres_connection_string" {
  type    = string
  default = ""
}

variable "posthog_api_key" {
  type    = string
  default = ""
}

variable "environment" {
  type    = string
  default = ""
}

variable "analytics_collector_host" {
  type    = string
  default = ""
}

variable "logs_collector_address" {
  type    = string
  default = ""
}

variable "analytics_collector_api_token" {
  type    = string
  default = ""
}

variable "otel_tracing_print" {
  type    = string
  default = ""
}

variable "loki_address" {
  type = string
  default = ""
}

variable "orchestrator_port" {
  type    = string
  default = ""
}

variable "session_proxy_port" {
  type    = string
  default = ""
}

variable "template_manager_address" {
  type    = string
  default = ""
}

variable "nomad_token" {
  type    = string
  default = ""
}

variable "otel_collector_grpc_endpoint" {
  type    = string
  default = ""
}

job "api" {
  datacenters = [var.gcp_zone]
  node_pool = "api"
  priority = 90

  group "api-service" {
    network {
      port "api" {
        static = var.api_port_number
      }
    }

    service {
      name = "api"
      port = var.api_port_number

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = var.api_port_number
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
        SESSION_PROXY_PORT            = var.session_proxy_port
        ORCHESTRATOR_PORT             = var.orchestrator_port
        TEMPLATE_MANAGER_ADDRESS      = var.template_manager_address
        POSTGRES_CONNECTION_STRING    = var.postgres_connection_string
        ENVIRONMENT                   = var.environment
        POSTHOG_API_KEY               = var.posthog_api_key
        ANALYTICS_COLLECTOR_HOST      = var.analytics_collector_host
        ANALYTICS_COLLECTOR_API_TOKEN = var.analytics_collector_api_token
        LOKI_ADDRESS                  = var.loki_address
        OTEL_TRACING_PRINT            = var.otel_tracing_print
        LOGS_COLLECTOR_ADDRESS        = var.logs_collector_address
        NOMAD_TOKEN                   = var.nomad_token
        OTEL_COLLECTOR_GRPC_ENDPOINT  = var.otel_collector_grpc_endpoint
        TEMPLATE_BUCKET_NAME          = "skip"
        ADMIN_TOKEN                   = var.admin_token
      }

      config {
        network_mode = "host"
        image        = var.image_name
        ports        = [var.api_port_name]
        args = [
          "--port", "${var.api_port_number}",
        ]
      }
    }
  }
}
