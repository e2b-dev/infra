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
  type    = number
  default = 0
}

variable "client_cluster_size_max" {
  type    = number
  default = 0
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

variable "build_cluster_size" {
  type = number
}

variable "build_machine_type" {
  type = string
}

variable "clickhouse_cluster_size" {
  type = number
}

variable "clickhouse_machine_type" {
  type = string
}

variable "clickhouse_database_name" {
  description = "The name of the ClickHouse database to create."
  type        = string
  default     = "default"
}

variable "clickhouse_job_constraint_prefix" {
  description = "The prefix to use for the job constraint of the instance in the metadata."
  type        = string
  default     = "clickhouse"
}

variable "clickhouse_node_pool" {
  description = "The name of the Nomad pool."
  type        = string
  default     = "clickhouse"
}

variable "clickhouse_server_service_port" {
  type = object({
    name = string
    port = number
  })
  default = {
    name = "clickhouse"
    port = 9000
  }
}


variable "clickhouse_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
  default = {
    name = "clickhouse-health"
    port = 8123
    path = "/health"
  }
}

variable "client_proxy_count" {
  type    = number
  default = 1
}


variable "client_proxy_resources_memory_mb" {
  type    = number
  default = 1024
}

variable "client_proxy_resources_cpu_count" {
  type    = number
  default = 1
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

variable "allow_sandbox_internet" {
  type    = bool
  default = true
}

variable "client_cluster_cache_disk_size_gb" {
  type        = number
  description = "The size of the root disk for the orchestrator machines in GB"
  default     = 500
}

variable "orchestrator_port" {
  type    = number
  default = 5008
}

variable "orchestrator_proxy_port" {
  type    = number
  default = 5007
}

variable "template_manager_port" {
  type    = number
  default = 5009
}

variable "environment" {
  type    = string
  default = "prod"
}

variable "otel_collector_resources_memory_mb" {
  type    = number
  default = 1024
}

variable "otel_collector_resources_cpu_count" {
  type    = number
  default = 0.5
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

variable "additional_domains" {
  type        = string
  description = "Additional domains which can be used to access the e2b cluster, separated by commas"
  default     = ""
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

variable "loki_resources_memory_mb" {
  type    = number
  default = 2048
}

variable "loki_resources_cpu_count" {
  type    = number
  default = 1
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
  default     = ""
}

variable "redis_managed" {
  default = false
  type    = bool
}

variable "grafana_managed" {
  default = false
  type    = bool
}

variable "write_clickhouse_metrics" {
  description = "Whether to write metrics to ClickHouse"
  type        = bool
  default     = false
}