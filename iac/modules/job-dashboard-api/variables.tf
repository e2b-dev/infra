variable "node_pool" {
  type = string
}

variable "update_stanza" {
  type = bool
}

variable "image" {
  type = string
}

variable "count_instances" {
  type = number
}

variable "job_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}
