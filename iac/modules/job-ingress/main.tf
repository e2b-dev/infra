resource "nomad_job" "ingress" {
  jobspec = templatefile("${path.module}/jobs/ingress.hcl", {
    count         = var.ingress_count
    node_pool     = var.node_pool
    update_stanza = var.update_stanza
    cpu_count     = var.ingress_cpu_count
    memory_mb     = var.ingress_memory_mb

    ingress_port = var.ingress_proxy_port
    control_port = var.ingress_control_port

    nomad_endpoint = var.nomad_endpoint
    nomad_token    = var.nomad_token

    consul_token    = var.consul_token
    consul_endpoint = var.consul_endpoint
  })
}

variable "nomad_token" {
  type = string
}

variable "nomad_endpoint" {
  type    = string
  default = "http://localhost:4646"
}

variable "consul_token" {
  type = string
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