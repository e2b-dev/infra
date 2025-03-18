resource "google_logging_metric" "ids_threat_detection" {
  project = var.gcp_project_id
  name    = "${var.prefix}ids-threat-detection-metric"

  filter = "logName=\"projects/${var.gcp_project_id}/logs/ids.googleapis.com%2Fthreat\" AND resource.type=\"ids.googleapis.com/Endpoint\" AND jsonPayload.alert_severity=(\"HIGH\" OR \"CRITICAL\")"
}

data "google_secret_manager_secret_version" "notification_email_address" {
  secret = var.notification_email_secret_version.secret

  depends_on = [var.notification_email_secret_version]
}

resource "google_monitoring_notification_channel" "email_channel" {
  project      = var.gcp_project_id
  display_name = "Email Notification Channel"

  type = "email"
  labels = {
    email_address = data.google_secret_manager_secret_version.notification_email_address.secret_data
  }
}

resource "google_monitoring_alert_policy" "ids_threat_alert" {
  project = var.gcp_project_id

  display_name = "IDS Threat Detection Alert"
  combiner     = "OR"

  conditions {
    display_name = "IDS Threat Condition"
    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.ids_threat_detection.name}\" AND resource.type=\"ids.googleapis.com/Endpoint\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"
      aggregations {
        alignment_period     = "60s"
        per_series_aligner   = "ALIGN_RATE"
        cross_series_reducer = "REDUCE_NONE"
      }
    }
  }

  notification_channels = [google_monitoring_notification_channel.email_channel.id]
}
