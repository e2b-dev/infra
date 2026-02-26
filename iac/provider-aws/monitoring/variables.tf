variable "prefix" {
  description = "Resource name prefix"
  type        = string
}

variable "enable_monitoring" {
  description = "Enable CloudWatch alarms and SNS alerting"
  type        = bool
  default     = false
}

variable "alert_email" {
  description = "Email address for CloudWatch alarm notifications"
  type        = string
  default     = ""
}

variable "monthly_budget_amount" {
  description = "Monthly AWS spending threshold in USD for billing alarm"
  type        = number
  default     = 1000
}

variable "eks_cluster_name" {
  description = "EKS cluster name for ContainerInsights metrics"
  type        = string
}

variable "redis_replication_group_id" {
  description = "ElastiCache replication group ID for Redis alarms"
  type        = string
  default     = ""
}

variable "alb_arn_suffix" {
  description = "ALB ARN suffix for CloudWatch metrics"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
