variable "provider_name" {
  type        = string
  description = "Cloud provider: gcp or aws"

  validation {
    condition     = contains(["gcp", "aws"], var.provider_name)
    error_message = "provider_name must be 'gcp' or 'aws'"
  }
}

variable "provider_gcp_config" {
  type = object({
    service_account_key = string
    project_id          = string
    region              = string
    docker_registry     = string
  })
  default = {
    service_account_key = ""
    project_id          = ""
    region              = ""
    docker_registry     = ""
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

variable "environment" {
  type = string
}

variable "domain_name" {
  type = string
}

variable "update_stanza" {
  type        = bool
  description = "Enable scaling, update block, and extended kill_timeout"
}

variable "artifact_source" {
  type        = string
  description = "Full artifact URL for the template-manager binary (e.g. gcs::https://... or s3::https://...)"
}

variable "template_manager_checksum" {
  type        = string
  description = "Hex checksum of the template-manager binary"
}

variable "api_secret" {
  type      = string
  sensitive = true
}

variable "consul_acl_token" {
  type      = string
  sensitive = true
}

variable "template_bucket_name" {
  type = string
}

variable "build_cache_bucket_name" {
  type    = string
  default = ""
}

variable "otel_collector_grpc_endpoint" {
  type = string
}

variable "logs_collector_address" {
  type = string
}

variable "orchestrator_services" {
  type    = string
  default = "template-manager"
}

variable "shared_chunk_cache_path" {
  type    = string
  default = ""
}

variable "clickhouse_connection_string" {
  type      = string
  default   = ""
  sensitive = true
}

variable "dockerhub_remote_repository_url" {
  type    = string
  default = ""
}

variable "redis_pool_size" {
  type    = number
  default = 10
}

variable "launch_darkly_api_key" {
  type      = string
  default   = ""
  sensitive = true
}

// Nomad API access for job count query
variable "nomad_addr" {
  type        = string
  description = "Nomad API address (e.g. https://nomad.example.com)"
}

variable "nomad_token" {
  type      = string
  sensitive = true
}

