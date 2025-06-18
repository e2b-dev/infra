variable "custom_envs_repository_name" {
  type = string
}

variable "prefix" {
  type        = string
  description = "The prefix to use for all resources in this module"
  default     = "e2b-"
}
