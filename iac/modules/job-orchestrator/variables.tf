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

variable "job_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}
