variable "prefix" {
  type = string
}

variable "environment" {
  description = "The environment (e.g. staging, prod)."
  type        = string
}

variable "domain_name" {
  type = string
}

variable "additional_domains" {
  type = list(string)
}

variable "cluster_tag_name" {
  type = string
}

variable "network_name" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "api_use_nat" {
  type = bool
}

variable "api_nat_ips" {
  type = list(string)
}

variable "api_nat_min_ports_per_vm" {
  type = number
}

variable "cloudflare_api_token_secret_name" {
  type = string
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "ingress_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "client_proxy_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

variable "client_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "nomad_port" {
  type = number
}

variable "api_instance_group" {
  type = string
}

variable "server_instance_group" {
  type = string
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
}

variable "additional_api_path_rules" {
  description = "Additional path rules to add to the load balancer routing."
  type = list(object({
    paths      = list(string)
    service_id = string
  }))
}

variable "additional_ports" {
  description = "Additional ports to expose on the load balancer."
  type        = list(number)
}
