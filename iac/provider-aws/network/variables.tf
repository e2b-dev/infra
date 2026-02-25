variable "prefix" {
  type = string
}

variable "availability_zones" {
  description = "List of availability zones to use"
  type        = list(string)
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "environment" {
  description = "Environment name (e.g. dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "cluster_name" {
  description = "EKS cluster name for subnet discovery tags"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
}

variable "enable_vpc_flow_logs" {
  description = "Enable VPC Flow Logs for network audit trail (GDPR + ISO 27001)"
  type        = bool
  default     = false
}

variable "vpc_flow_logs_retention_days" {
  description = "CloudWatch log group retention in days for VPC Flow Logs"
  type        = number
  default     = 90
}
