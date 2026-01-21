variable "bucket_name" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "labels" {
  type = map(string)
}

variable "service_account_email" {
  type = string
}
