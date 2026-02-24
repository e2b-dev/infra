variable "prefix" {
  type = string
}

variable "bucket_prefix" {
  type = string
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
}

variable "aws_region" {
  type = string
}

variable "template_bucket_name" {
  type        = string
  description = "The name of the FC template bucket"
  default     = ""
}
