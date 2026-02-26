output "sns_topic_arn" {
  description = "SNS topic ARN for alarm notifications"
  value       = var.enable_monitoring ? aws_sns_topic.alerts[0].arn : ""
}
