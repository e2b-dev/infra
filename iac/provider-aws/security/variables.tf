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

variable "enable_guardduty" {
  description = "Enable AWS GuardDuty for threat detection (ISO 27001)"
  type        = bool
  default     = true
}

variable "enable_aws_config" {
  description = "Enable AWS Config for configuration compliance monitoring (ISO 27001)"
  type        = bool
  default     = true
}

variable "enable_inspector" {
  description = "Enable AWS Inspector v2 for vulnerability scanning (ISO 27001)"
  type        = bool
  default     = true
}

variable "enable_cloudtrail" {
  description = "Enable AWS CloudTrail for API audit logging (ISO 27001 / SOC2)"
  type        = bool
  default     = true
}
