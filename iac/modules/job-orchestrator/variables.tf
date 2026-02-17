variable "provider_name" {
  type        = string
  description = "Cloud provider: gcp or aws"

  validation {
    condition     = contains(["gcp", "aws"], var.provider_name)
    error_message = "provider_name must be 'gcp' or 'aws'"
  }
}

variable "provider_aws_config" {
  type = object({
    region                 = string
    docker_repository_name = string
  })
  default = {
    region                 = ""
    docker_repository_name = ""
  }
}

variable "node_pool" {
  type = string
}

variable "port" {
  type = number
}

variable "proxy_port" {
  type = number
}

variable "environment" {
  type = string
}

variable "artifact_source" {
  type        = string
  description = "Full artifact URL for the orchestrator binary (e.g. gcs::https://... or s3::https://...)"
}

variable "orchestrator_checksum" {
  type        = string
  description = "Hex checksum of the orchestrator binary, used for change detection"
}

# Env vars - required
variable "logs_collector_address" {
  type = string
}

variable "otel_collector_grpc_endpoint" {
  type = string
}

variable "envd_timeout" {
  type = string
}

variable "template_bucket_name" {
  type = string
}

variable "allow_sandbox_internet" {
  type = string
}

variable "clickhouse_connection_string" {
  type      = string
  default   = ""
  sensitive = true
}

variable "redis_url" {
  type      = string
  sensitive = true
}

variable "redis_cluster_url" {
  type      = string
  sensitive = true
}

variable "redis_tls_ca_base64" {
  type      = string
  default   = ""
  sensitive = true
}

variable "consul_token" {
  type      = string
  sensitive = true
}

variable "domain_name" {
  type = string
}

variable "shared_chunk_cache_path" {
  type    = string
  default = ""
}

variable "launch_darkly_api_key" {
  type      = string
  default   = ""
  sensitive = true
}

variable "orchestrator_services" {
  type    = string
  default = "orchestrator"
}

variable "build_cache_bucket_name" {
  type    = string
  default = ""
}

variable "use_local_namespace_storage" {
  type    = bool
  default = false
}
