variable "gcp_project_id" {
  description = "The project to deploy the cluster in"
  type        = string
}

variable "gcp_region" {
  type = string
}

variable "gcp_zone" {
  description = "All GCP resources will be launched in this Zone."
  type        = string
}

variable "server_cluster_size" {
  type = number
}

variable "server_machine_type" {
  type = string
}

variable "client_cluster_size" {
  type = number
}

variable "client_cluster_auto_scaling_max" {
  type = number
}

variable "client_machine_type" {
  type = string
}

variable "api_cluster_size" {
  type = number
}

variable "api_machine_type" {
  type = string
}

variable "client_proxy_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
  default = {
    name = "health"
    port = 3001
    path = "/health"
  }
}

variable "client_proxy_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "session"
    port = 3002
  }
}

variable "session_proxy_service_name" {
  type    = string
  default = "session-proxy"
}

variable "session_proxy_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "session"
    port = 3003
  }
}

variable "logs_proxy_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "logs"
    port = 30006
  }
}

variable "logs_health_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "logs-health"
    port        = 44313
    health_path = "/health"
  }
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "api"
    port        = 50001
    health_path = "/health"
  }
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
  default = {
    name        = "docker-reverse-proxy"
    port        = 5000
    health_path = "/health"
  }
}

variable "redis_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "redis"
    port = 6379
  }
}

variable "nomad_port" {
  type    = number
  default = 4646
}

variable "orchestrator_port" {
  type    = number
  default = 5008
}

variable "template_manager_port" {
  type    = number
  default = 5009
}

variable "environment" {
  type    = string
  default = "prod"
}

variable "otel_tracing_print" {
  description = "Whether to print OTEL traces to stdout"
  type        = bool
  default     = false
}

variable "domain_name" {
  type        = string
  description = "The domain name where e2b will run"
}

variable "prefix" {
  type        = string
  description = "The prefix to use for all resources in this module"
  default     = "e2b-"
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
  default = {
    "app"       = "e2b"
    "terraform" = "true"
  }
}

variable "terraform_state_bucket" {
  description = "The name of the bucket to store terraform state in"
  type        = string
}

variable "loki_service_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "loki"
    port = 3100
  }
}

variable "template_bucket_location" {
  type        = string
  description = "The location of the FC template bucket"
}

variable "template_bucket_name" {
  type        = string
  description = "The name of the FC template bucket"
}
