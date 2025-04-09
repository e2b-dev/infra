variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "gcp_project_id" {
  type = string
}

variable "domain_name" {
  type = string
}

variable "panel_directory_name" {
  description = "Path to the directory containing panel files"
  type        = string
  default     = "panels"
}
