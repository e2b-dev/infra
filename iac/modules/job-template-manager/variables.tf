variable "node_pool" {
  type = string
}

variable "port" {
  type = number
}

variable "update_stanza" {
  type        = bool
  description = "Enable scaling, update block, and extended kill_timeout"
}

variable "artifact_source" {
  type        = string
  description = "Full artifact URL for the template-manager binary (e.g. gcs::https://... or s3::https://...)"
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

variable "job_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}
