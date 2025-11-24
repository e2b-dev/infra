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