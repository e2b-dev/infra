variable "prefix" {
  type = string
}

variable "gcp_project_id" {
  type = string
}

variable "domain_name" {
  type = string
}

variable "panels_directory_name" {
  description = "Path to the directory containing panel files"
  type        = string
  default     = "panels"
}

variable "dashboards_directory_name" {
  description = "Path to the directory containing dashboard files"
  type        = string
  default     = "dashboards"
}