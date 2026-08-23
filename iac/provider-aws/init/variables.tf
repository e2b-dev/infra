variable "prefix" {
  type = string
}

variable "bucket_prefix" {
  type = string
}

variable "allow_force_destroy" {
  default = false
}

variable "docker_reverse_proxy_enabled" {
  type    = bool
  default = true
}

variable "region" {
  type = string
}

variable "endpoint_ingress_subnet_ids" {
  type = list(string)
}
