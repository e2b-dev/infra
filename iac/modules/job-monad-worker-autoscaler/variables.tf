variable "node_pool" {
  type        = string
  description = "Nomad API-node pool that hosts the redundant shadow observers."
}

variable "worker_node_pool" {
  type        = string
  description = "Nomad worker pool counted independently from TAMS."
}

variable "worker_cluster_keys" {
  type        = list(string)
  description = "Terraform client-cluster keys represented by the counted Nomad worker pool."

  validation {
    condition     = length(var.worker_cluster_keys) > 0 && alltrue([for key in var.worker_cluster_keys : trimspace(key) != ""])
    error_message = "worker_cluster_keys must contain non-empty Terraform client-cluster keys."
  }
}

variable "worker_cluster_size" {
  type        = number
  description = "Terraform floor of the isolated default worker MIG."
}

variable "worker_machine_type" {
  type        = string
  description = "GCE machine profile whose measured density drives the capacity formula."
}

variable "allocation_count" {
  type        = number
  description = "Number of redundant observers; Consul elects one capacity reader."
  default     = 2

  validation {
    condition     = contains([1, 2], var.allocation_count)
    error_message = "The shadow controller supports one or two allocations only."
  }
}

variable "artifact_source" {
  type        = string
  description = "Immutable GCS artifact URL for the shadow controller binary."

  validation {
    condition     = can(regex("^gcs::https://.+monad-worker-autoscaler\\.[0-9a-f]{12,40}#.+$", var.artifact_source))
    error_message = "artifact_source must name an immutable monad-worker-autoscaler.<git-sha> GCS object and generation."
  }
}

variable "tams_capacity_url" {
  type        = string
  description = "Exact authenticated TAMS /v1/ops/capacity endpoint."

  validation {
    condition     = can(regex("^https://[^/?#]+/v1/ops/capacity$", var.tams_capacity_url))
    error_message = "tams_capacity_url must be an HTTPS /v1/ops/capacity URL without query or fragment."
  }
}

variable "tams_audience" {
  type        = string
  description = "Expected HTTPS audience for attached-service-account identity tokens."

  validation {
    condition     = can(regex("^https://[^/?#]+$", var.tams_audience))
    error_message = "tams_audience must be an HTTPS URI without query or fragment."
  }
}

variable "nomad_token" {
  type      = string
  sensitive = true
}

variable "consul_token" {
  type      = string
  sensitive = true
}

variable "metrics_port" {
  type    = number
  default = 9464

  validation {
    condition     = var.metrics_port >= 1024 && var.metrics_port <= 65535
    error_message = "metrics_port must be between 1024 and 65535."
  }
}
