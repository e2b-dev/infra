variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "network_name" {
  description = "Name of the VPC where Cloud SQL will get a private IP (harness-vpc). Peering range must already exist on the VPC."
  type        = string
}

variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "postgres_connection_string_secret_name" {
  description = "Fully-qualified Secret Manager secret name (from module.init.postgres_connection_string_secret_name) where the real connection string is published."
  type        = string
}

variable "labels" {
  type    = map(string)
  default = {}
}
