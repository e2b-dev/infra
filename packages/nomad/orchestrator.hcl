variable "gcp_zone" {
  type    = string
}

variable "port" {
  type    = number
  default = 5008
}

variable "consul_token" {
  type    = string
  default = ""
}

variable "memory_mb" {
  type    = number
  default = 1024
}

variable "cpu_mhz" {
  type    = number
  default = 1000
}

variable "logs_proxy_address" {
  type    = string
  default = ""
}

variable "logs_collector_public_ip" {
  type    = string
  default = ""
}

variable "otel_tracing_print" {
  type    = string
  default = ""
}

variable "environment" {
  type    = string
  default = ""
}

variable "bucket_name" {
    type    = string
    default = ""
}

variable "orchestrator_checksum" {
  type    = string
  default = ""
}

variable "template_bucket_name" {
  type    = string
  default = ""
}

variable "otel_collector_grpc_endpoint" {
  type    = string
  default = ""
}

job "orchestrator" {
  type = "system"
  datacenters = [var.gcp_zone]

  priority = 90

  group "client-orchestrator" {
    network {
      port "orchestrator" {
        static = var.port
      }
    }

    service {
      name = "orchestrator"
      port = var.port

      check {
        type         = "grpc"
        name         = "health"
        interval     = "20s"
        timeout      = "5s"
        grpc_use_tls = false
        port         = var.port
      }
    }

    task "start" {
      driver = "raw_exec"

      resources {
        memory     = var.memory_mb
        cpu        = var.cpu_mhz
      }

      env {
        NODE_ID                      = "${node.unique.id}"
        CONSUL_TOKEN                 = var.consul_token
        OTEL_TRACING_PRINT           = var.otel_tracing_print
        LOGS_COLLECTOR_ADDRESS       = var.logs_proxy_address
        LOGS_COLLECTOR_PUBLIC_IP       = var.logs_collector_public_ip
        ENVIRONMENT                  = var.environment
        TEMPLATE_BUCKET_NAME         = var.template_bucket_name
        OTEL_COLLECTOR_GRPC_ENDPOINT = var.otel_collector_grpc_endpoint
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " chmod +x local/orchestrator && local/orchestrator --port ${var.port}"]
      }

      artifact {
        source      = "gcs::https://www.googleapis.com/storage/v1/${var.bucket_name}/orchestrator"
        options {
            checksum    = "md5:${var.orchestrator_checksum}"
        }
      }
    }
  }
}
