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
