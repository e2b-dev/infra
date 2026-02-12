variable "prefix" {
  type = string
}

variable "bucket_prefix" {
  type = string
}

variable "labels" {
  description = "The labels to attach to resources created by this module"
  type        = map(string)
}

variable "gcp_project_id" {
  description = "The project to deploy the cluster in"
  type        = string
}

variable "gcp_region" {
  type = string
}

variable "template_bucket_location" {
  type        = string
  description = "The location of the FC template bucket"
}

variable "template_bucket_name" {
  type        = string
  description = "The name of the FC template bucket"
  default     = ""
}
