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
  description = "DockerHub username for authenticated pulls through the remote repository. Leave empty to use unauthenticated access."
  default     = ""
}

variable "dockerhub_auth_password" {
  type        = string
  description = "DockerHub password or personal access token for authenticated pulls. Stored in GCP Secret Manager."
  sensitive   = true
  default     = ""
}
