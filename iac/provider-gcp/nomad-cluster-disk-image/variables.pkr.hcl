variable "gcp_project_id" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "network_name" {
  type = string
}

variable "subnet_name" {
  type = string
}

variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "consul_version" {
  type    = string
  default = "1.21.5"
}

variable "nomad_version" {
  type    = string
  default = "1.10.5"
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

variable "source_image" {
  type    = string
  default = "ubuntu-2404-noble-amd64-v20260517"
}
