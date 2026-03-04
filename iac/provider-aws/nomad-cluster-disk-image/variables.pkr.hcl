variable "aws_region" {
  type = string
}

variable "aws_profile" {
  type = string
}

variable "prefix" {
  type = string
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

variable "base_instance_type" {
  type    = string
  default = "t3.large"
}
