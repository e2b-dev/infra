variable "aws_region" {
  type = string
}

variable "aws_profile" {
  type = string
}

variable "source_ami_filter_name" {
  type = string
  default = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
}

variable "prefix" {
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

variable "base_instance_type" {
  type    = string
  default = "t3.large"
}

variable "vpc_id" {
  type    = string
  default = ""
}

variable "subnet_id" {
  type    = string
  default = ""
}
