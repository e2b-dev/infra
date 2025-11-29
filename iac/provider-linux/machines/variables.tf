variable "datacenter" { type = string }

variable "servers" {
  type = list(object({
    host                 = string
    ssh_user             = string
    ssh_private_key_path = string
  }))
}

variable "clients" {
  type = list(object({
    host                 = string
    ssh_user             = string
    ssh_private_key_path = string
    node_pool            = string
  }))
}

variable "consul_acl_token" {
  type    = string
  default = ""
}

variable "nomad_acl_token" {
  type    = string
  default = ""
}

variable "docker_image_prefix" {
  type    = string
  default = ""
}

variable "docker_http_proxy" {
  type    = string
  default = ""
}

variable "docker_https_proxy" {
  type    = string
  default = ""
}

variable "docker_no_proxy" {
  type    = string
  default = ""
}

variable "builder_node_pool" {
  type    = string
  default = ""
}

variable "orchestrator_node_pool" {
  type    = string
  default = ""
}

variable "kernel_source_base_url" {
  type = string
}

variable "firecracker_source_base_url" {
  type = string
}

variable "default_kernel_version" {
  type = string
}

variable "default_firecracker_version" {
  type = string
}

variable "fc_artifact_node_pools" {
  type = list(string)
}
