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

variable "dockerhub_username_secret_name" {
  type = string
}

variable "dockerhub_password_secret_name" {
  type = string
}

variable "dockerhub_upstream_allow_anonymous" {
  type        = bool
  description = "Explicitly accept anonymous, rate-limited Docker Hub pulls through this mirror when the dockerhub_username/password secrets are still the Terraform-managed placeholder. Default false: the mirror's lifecycle precondition fails the plan/apply loudly instead. Set true only as a deliberate, reviewed choice (e.g. to unblock docker.io-based project templates before real Docker Hub credentials are provisioned)."
  default     = false
}
