variable "node_pool" {
  type = string
}

variable "update_stanza" {
  type = bool
}

variable "prevent_colocation" {
  type    = bool
  default = false
}

variable "count_instances" {
  type = number
}

variable "memory_mb" {
  type = number
}

variable "cpu_count" {
  type = number
}

variable "port_name" {
  type = string
}

variable "port_number" {
  type = number
}

variable "api_internal_grpc_port" {
  type    = number
  default = 5009
}

variable "api_docker_image" {
  type = string
}

variable "db_migrator_docker_image" {
  type = string
}

variable "job_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}

variable "db_migrator_env_vars" {
  type      = map(string)
  default   = {}
  sensitive = true
}
