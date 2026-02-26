variable "prefix" {
  description = "Resource name prefix"
  type        = string
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "aurora_host" {
  description = "Aurora PostgreSQL cluster endpoint"
  type        = string

  validation {
    condition     = var.aurora_host != ""
    error_message = "aurora_host must be set when temporal module is enabled. Provide the Aurora cluster endpoint."
  }
}

variable "aurora_port" {
  description = "Aurora PostgreSQL port"
  type        = number
  default     = 5432
}

variable "temporal_db_user" {
  description = "PostgreSQL user for Temporal databases"
  type        = string
  default     = "temporal"
}

variable "temporal_chart_version" {
  description = "Temporal Helm chart version. Pin to a specific version for reproducible deploys."
  type        = string
  default     = "1.2.1"
}

variable "temporal_cert_validity_hours" {
  description = "Validity period in hours for Temporal mTLS certificates (default: 1 year)"
  type        = number
  default     = 8760
}

variable "temporal_worker_replica_count" {
  description = "Number of Temporal worker replicas"
  type        = number
  default     = 2
}

variable "temporal_web_replica_count" {
  description = "Number of Temporal web UI replicas"
  type        = number
  default     = 2
}
