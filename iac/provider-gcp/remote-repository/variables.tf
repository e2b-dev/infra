variable "prefix" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "google_service_account_email" {
  type = string
}

variable "dockerhub_auth_username" {
  type        = string
  description = "DockerHub username for authenticated pulls through the remote repository. Leave empty to use unauthenticated access. The password must be added manually as a secret version in GCP Secret Manager."
  default     = ""
}
