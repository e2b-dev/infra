variable "gcp_project_id" {
  description = "The project to deploy the cluster in"
  type        = string
}

variable "gcp_region" {
  description = "The region e2b resources will run in"
  type        = string
}

variable "gcp_zone" {
  description = "The zone e2b resources will run in"
  type        = string
}

variable "github_organization" {
  description = "The name of the github organization"
  type        = string
  default     = "e2b-dev"
}

variable "github_repository" {
  description = "The name of the repository"
  type        = string
  default     = "infra"
}

variable "prefix" {
  description = "The prefix to use for all resources in this module"
  type        = string
}

variable "terraform_state_bucket" {
  description = "The name of the bucket to store terraform state in"
  type        = string
}

variable "domain_name" {
  type = string
}
