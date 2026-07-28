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

variable "image_name" {
  type = string
}

variable "image_family" {
  type = string
}

variable "image_environment" {
  type = string
}

variable "source_revision" {
  type = string
}

variable "build_manifest_path" {
  type = string
}

variable "consul_version" {
  type    = string
  default = "1.17.3"
}

variable "nomad_version" {
  type    = string
  default = "1.8.4"
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
  default = "ubuntu-2404-noble-amd64-v20260723"
}
