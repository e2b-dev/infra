resource "google_filestore_instance" "slab-cache" {
  name        = var.name
  description = "High performance slab cache"
  tier        = var.tier
  protocol    = "NFS_V4_1"

  deletion_protection_enabled = true
  deletion_protection_reason  = "If this gets removed, the orchestrator will throw tons of errors"

  file_shares {
    capacity_gb = var.capacity_gb
    name        = "slabs"
  }

  networks {
    modes = [
      "MODE_IPV4",
    ]
    network = var.network_name
  }
}

data "google_monitoring_notification_channel" "notification" {
  count        = var.notification_display_name == null ? 0 : 1
  display_name = var.notification_display_name
}

resource "google_monitoring_alert_policy" "warning" {
  count = var.free_space_warning_threshold == 0 ? 0 : 1

  combiner = "OR"

  display_name = "memory-cache-disk-usage-low"

  notification_channels = var.notification_display_name == null ? [] : [
    data.google_monitoring_notification_channel.notification[0].id
  ]

  severity = "WARNING"

  alert_strategy {
    notification_prompts = [
      "OPENED",
      "CLOSED",
    ]
  }

  conditions {
    display_name = "Over ${var.free_space_warning_threshold}% of the memory cache disk has been used"

    condition_threshold {
      comparison      = "COMPARISON_GT"
      duration        = "0s"
      filter          = <<EOT
resource.type = "filestore_instance"
AND metric.type = "file.googleapis.com/nfs/server/used_bytes_percent"
AND metric.labels.file_share = "${google_filestore_instance.slab-cache.file_shares[0].name}"
EOT
      threshold_value = var.free_space_warning_threshold

      aggregations {
        alignment_period   = "300s"
        group_by_fields    = []
        per_series_aligner = "ALIGN_MAX"
      }

      trigger {
        count   = 1
        percent = 0
      }
    }
  }

  documentation {
    mime_type = "text/markdown"
    subject   = "Memory cache disk usage has gone over ${var.free_space_warning_threshold}%"
    content   = "Your memory cache filestore instance disk usage has gone over ${var.free_space_warning_threshold}%. "
  }
}

resource "google_monitoring_alert_policy" "error" {
  count = var.free_space_error_threshold == 0 ? 0 : 1

  combiner = "OR"

  display_name = "memory-cache-disk-usage-very-low"

  notification_channels = var.notification_display_name == null ? [] : [
    data.google_monitoring_notification_channel.notification[0].id
  ]

  severity = "ERROR"

  alert_strategy {
    notification_prompts = [
      "OPENED",
      "CLOSED",
    ]
  }

  conditions {
    display_name = "Over ${var.free_space_error_threshold}% of the memory cache disk has been used"

    condition_threshold {
      comparison      = "COMPARISON_GT"
      duration        = "0s"
      filter          = <<EOT
resource.type = "filestore_instance"
AND metric.type = "file.googleapis.com/nfs/server/used_bytes_percent"
AND metric.labels.file_share = "${google_filestore_instance.slab-cache.file_shares[0].name}"
EOT
      threshold_value = var.free_space_error_threshold

      aggregations {
        alignment_period   = "300s"
        group_by_fields    = []
        per_series_aligner = "ALIGN_MAX"
      }

      trigger {
        count   = 1
        percent = 0
      }
    }
  }

  documentation {
    mime_type = "text/markdown"
    subject   = "Memory cache disk usage has gone over ${var.free_space_error_threshold}%"
    content   = "Your memory cache filestore instance disk usage has gone over ${var.free_space_error_threshold}%. "
  }
}
