variable "node_pool" {
  type = string
}

variable "image" {
  type    = string
  default = "gcr.io/google.com/cloudsdktool/google-cloud-cli:alpine"
}

variable "gcp_project_id" {
  type = string
}

variable "ca_pool" {
  type = string
}

variable "ca_id" {
  type = string
}

variable "ca_location" {
  type = string
}

variable "server_name" {
  type = string
}

variable "cert_validity" {
  type = string
}

variable "renew_interval" {
  type = string
}

variable "certificate_consul_key" {
  type = string
}

variable "private_key_consul_key" {
  type = string
}

variable "client_ca_consul_key" {
  type = string
}

variable "reload_consul_key" {
  type = string
}

variable "consul_endpoint" {
  type = string
}

variable "consul_token" {
  type      = string
  sensitive = true
}
