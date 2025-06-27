variable "prefix" {
  type = string
}

variable "google_service_account_email" {
  type = string
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
}
