variable "name" {
  description = "The name of the Nomad cluster (e.g. nomad-stage). This variable is used to namespace all resources created by this module."
  type        = string
}

variable "network_name" {
  description = "The name of the VPC Network where all resources should be created."
  type        = string
}

variable "tier" {
  type = string
}

variable "capacity_gb" {
  type = number
}

variable "notification_display_name" {
  type = string
}

variable "free_space_warning_threshold" {
  type = number
}

variable "free_space_error_threshold" {
  type = number
}
