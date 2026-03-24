variable "prefix" {
  type = string
}

variable "bucket_prefix" {
  type = string
}

variable "allow_force_destroy" {
  default = false
}

variable "region" {
  type = string
}

variable "endpoint_ingress_subnet_ids" {
  type = list(string)
}
