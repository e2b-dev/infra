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

variable "enable_s3_access_logging" {
  description = "Enable S3 server access logging for compliance-sensitive buckets"
  type        = bool
  default     = false
}

variable "s3_kms_key_arn" {
  description = "KMS CMK ARN for S3 bucket encryption. When set, buckets use SSE-KMS instead of SSE-S3."
  type        = string
  default     = ""
}
