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

variable "mode" {
  type        = string
  description = "Controller phase: non-mutating shadow observer or scale-out-only actuation."
  default     = "shadow"

  validation {
    condition     = contains(["shadow", "scale-out"], var.mode)
    error_message = "mode must be shadow or scale-out."
  }
}

variable "worker_host_floor" {
  type        = number
  description = "Reviewed worker-host floor; must equal the Terraform client cluster_size and is never resized below."

  validation {
    condition     = var.worker_host_floor >= 2 && var.worker_host_floor <= 15 && floor(var.worker_host_floor) == var.worker_host_floor
    error_message = "worker_host_floor must be an integer from 2 to 15."
  }
}

variable "mig_project_id" {
  type        = string
  description = "GCP project of the worker managed instance group; scale-out mode only."
  default     = ""

  validation {
    condition     = var.mig_project_id == "" || can(regex("^[a-z][-a-z0-9]{4,28}[a-z0-9]$", var.mig_project_id))
    error_message = "mig_project_id must be empty or a valid GCP project id."
  }
}

variable "mig_region" {
  type        = string
  description = "GCP region of the worker managed instance group; scale-out mode only."
  default     = ""

  validation {
    condition     = var.mig_region == "" || can(regex("^[a-z]+-[a-z0-9]+[0-9]$", var.mig_region))
    error_message = "mig_region must be empty or a valid GCP region."
  }
}

variable "mig_name" {
  type        = string
  description = "Name of the worker managed instance group; scale-out mode only."
  default     = ""

  validation {
    condition     = var.mig_name == "" || can(regex("^[a-z](?:[-a-z0-9]{0,61}[a-z0-9])?$", var.mig_name))
    error_message = "mig_name must be empty or a valid GCE resource name."
  }
}
