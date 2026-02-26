output "guardduty_detector_id" {
  value = var.enable_guardduty ? aws_guardduty_detector.main[0].id : ""
}

output "config_recorder_name" {
  value = var.enable_aws_config ? aws_config_configuration_recorder.main[0].name : ""
}

output "config_bucket_name" {
  value = var.enable_aws_config ? aws_s3_bucket.config[0].id : ""
}

output "inspector_enabled" {
  value = var.enable_inspector
}

output "cloudtrail_arn" {
  value = var.enable_cloudtrail ? aws_cloudtrail.main[0].arn : ""
}

output "cloudtrail_bucket_name" {
  value = var.enable_cloudtrail ? aws_s3_bucket.cloudtrail[0].id : ""
}

output "s3_kms_key_arn" {
  description = "KMS CMK ARN for S3 bucket encryption"
  value       = aws_kms_key.s3.arn
}
