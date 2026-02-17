variable "nomad_token" {
  type      = string
  sensitive = true
}

variable "nomad_endpoint" {
  type    = string
  default = "http://localhost:4646"
}

variable "consul_token" {
  type      = string
  sensitive = true
}

variable "consul_endpoint" {
  type    = string
  default = "http://localhost:8500"
}

variable "ingress_proxy_port" {
  type = number
}

variable "ingress_control_port" {
  type    = number
  default = 8900
}

variable "node_pool" {
  type = string
}

variable "update_stanza" {
  type = bool
}

variable "ingress_count" {
  type = number
}

variable "ingress_cpu_count" {
  type    = number
  default = 1
}

variable "ingress_memory_mb" {
  type    = number
  default = 512
}