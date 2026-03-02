variable "prefix" {
  type = string
}

variable "name" {
  type    = string
  default = "valkey"
}

variable "description" {
  type    = string
  default = "valkey cluster"
}

variable "vpc_id" {
  type = string
}

variable "port" {
  type = number
}

variable "replica_size" {
  type = number
}

variable "instance_type" {
  type = string
}

variable "subnet_group_name" {
  type = string
}

variable "ingress_security_group_ids" {
  type = list(string)
}