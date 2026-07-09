variable "update_stanza" {
  type = bool
}

variable "client_proxy_count" {
  type = number
}

variable "client_proxy_cpu_count" {
  type    = number
  default = 1
}

variable "client_proxy_memory_mb" {
  type    = number
  default = 512
}

variable "client_proxy_update_max_parallel" {
  type    = number
  default = 1
}

variable "node_pool" {
  type = string
}

variable "proxy_port" {
  type    = number
  default = 3002
}

variable "health_port" {
  type    = number
  default = 3001
}

variable "image" {
  type = string
}

variable "exposure_type" {
  type        = string
  default     = "public"
  description = "Exposure type: public, private, or both"
  validation {
    condition     = contains(["public", "private", "both"], var.exposure_type)
    error_message = "Must be: public, private, or both"
  }
}

variable "job_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}
