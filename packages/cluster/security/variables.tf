variable "prefix" {
  type = string
}

variable "environment" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_zone" {
  type = string
}

variable "vpc_network_name" {
  type = string
}

variable "notification_email_secret_version" {
  type = any
}
