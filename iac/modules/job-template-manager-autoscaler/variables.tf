variable "node_pool" {
  type = string
}

variable "autoscaler_version" {
  type        = string
  description = "Version of the Nomad Autoscaler to deploy"
  default     = "0.4.5"
}

variable "nomad_token" {
  type      = string
  sensitive = true
}

variable "apm_plugin_artifact_source" {
  type        = string
  description = "Full artifact URL for the nomad-nodepool-apm plugin"
}

variable "apm_plugin_checksum" {
  type        = string
  description = "Hex checksum of the nomad-nodepool-apm plugin"
}
