variable "gcp_project_id" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "network_name" {
  type = string
}

variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "consul_version" {
  type    = string
  default = "1.16.2"
}

variable "nomad_version" {
  type    = string
  default = "1.6.2"
}

variable "vault_version" {
  type    = string
  default = "1.20.3"
}

# Keep in sync with `clickhouse_version` in iac/modules/job-clickhouse/variables.tf
variable "clickhouse_client_version" {
  type    = string
  default = "25.4.5.24"
}

variable "cni_plugin_version" {
  type    = string
  default = "v1.6.2"
}