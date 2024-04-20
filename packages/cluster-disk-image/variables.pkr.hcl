variable "aws_region" {
  type    = string
}

variable "aws_access_key" {
  type    = string
}

variable "aws_secret_key" {
  type    = string
}

variable "consul_version" {
  type    = string
  default = "1.16.2"
}

variable "nomad_version" {
  type    = string
  default = "1.6.2"
}
