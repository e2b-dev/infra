variable "provider_name" {
  type        = string
  description = "Cloud provider: gcp or aws"

  validation {
    condition     = contains(["gcp", "aws"], var.provider_name)
    error_message = "provider_name must be 'gcp' or 'aws'"
  }
}

variable "node_pool" {
  type = string
}

variable "job_constraint_prefix" {
  type = string
}

variable "server_count" {
  type = number
}

// ---
// ClickHouse server
// ---

variable "server_secret" {
  type = string
}

variable "clickhouse_version" {
  type    = string
  default = "25.4.5.24"
}

variable "cpu_count" {
  type    = number
  default = 1
}

variable "memory_mb" {
  type    = number
  default = 512
}

variable "clickhouse_database" {
  type = string
}

variable "clickhouse_username" {
  type = string
}

variable "clickhouse_password" {
  type      = string
  sensitive = true
}

variable "clickhouse_port" {
  type = number
}

variable "clickhouse_metrics_port" {
  type = number
}

variable "otel_exporter_endpoint" {
  type        = string
  description = "OTLP exporter endpoint (e.g. http://localhost:4317)"
}

// ---
// Backup / restore
// ---

variable "clickhouse_backup_version" {
  type    = string
  default = "2.6.22"
}

variable "backup_bucket" {
  type    = string
  default = ""
}

variable "backup_folder" {
  type    = string
  default = "clickhouse-data"
}

variable "gcs_credentials_json_encoded" {
  type      = string
  default   = ""
  sensitive = true
}

variable "aws_region" {
  type    = string
  default = ""
}

// ---
// Migrator
// ---

variable "clickhouse_migrator_image" {
  type = string
}
