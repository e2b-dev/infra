variable "gcp_project_id" {
  type        = string
  description = "The GCP project ID"
}

variable "gcp_region" {
  type        = string
  description = "The GCP region"
}

variable "gcp_zone" {
  type        = string
  description = "The GCP zone"
}

variable "gcp_service_account_email" {
  type        = string
  description = "The GCP service account email"
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
}

variable "fc_template_bucket_location" {
  type        = string
  description = "The location of the FC template bucket"
}

variable "fc_template_bucket_name" {
  type        = string
  description = "The name of the FC template bucket"
}
