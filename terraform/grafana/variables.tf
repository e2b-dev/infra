variable "grafana_managed" {
  type    = bool
  default = false
}

variable "prefix" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "domain_name" {
  type = string
}

variable "gcp_region" {
  type = string
}
