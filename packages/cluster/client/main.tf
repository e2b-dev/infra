resource "google_compute_health_check" "nomad_check" {
  name                = "${var.cluster_name}-nomad-client-check"
  check_interval_sec  = 15
  timeout_sec         = 10
  healthy_threshold   = 2
  unhealthy_threshold = 10 # 50 seconds

  log_config {
    enable = true
  }

  http_health_check {
    request_path = "/v1/agent/health"
    port         = var.nomad_port
  }
}

resource "google_compute_autoscaler" "default" {
  provider = google-beta

  name   = "${var.cluster_name}-autoscaler"
  zone   = var.gcp_zone
  target = google_compute_instance_group_manager.client_cluster.id

  autoscaling_policy {
    max_replicas    = var.cluster_size + var.cluster_auto_scaling_max
    min_replicas    = var.cluster_size
    cooldown_period = 240
    mode            = "ONLY_SCALE_OUT"

    cpu_utilization {
      target = 0.6
    }
  }
}

resource "google_compute_instance_group_manager" "client_cluster" {
  name = "${var.cluster_name}-ig"

  version {
    name              = google_compute_instance_template.client.id
    instance_template = google_compute_instance_template.client.id
  }

  named_port {
    name = var.logs_health_proxy_port.name
    port = var.logs_health_proxy_port.port
  }

  named_port {
    name = var.logs_proxy_port.name
    port = var.logs_proxy_port.port
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.nomad_check.id
    initial_delay_sec = 600
  }

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                    = var.instance_group_update_policy_type
    minimal_action          = var.instance_group_update_policy_minimal_action
    max_surge_fixed         = var.instance_group_update_policy_max_surge_fixed
    max_surge_percent       = var.instance_group_update_policy_max_surge_percent
    max_unavailable_fixed   = var.instance_group_update_policy_max_unavailable_fixed
    max_unavailable_percent = var.instance_group_update_policy_max_unavailable_percent
    replacement_method      = "SUBSTITUTE"
  }

  base_instance_name = var.cluster_name
  target_pools       = var.instance_group_target_pools

  depends_on = [
    google_compute_instance_template.client,
  ]
}

data "google_compute_image" "source_image" {
  family = var.image_family
}


resource "google_compute_instance_template" "client" {
  name_prefix = "${var.cluster_name}-"

  instance_description = var.cluster_description
  machine_type         = var.machine_type
  min_cpu_platform     = "Intel Skylake"

  labels = merge(
    var.labels,
    (var.environment == "prod" ? {
      goog-ops-agent-policy = "v2-x86-template-1-2-0-${var.gcp_zone}"
    } : {})
  )
  tags                    = concat([var.cluster_tag_name], var.custom_tags)
  metadata_startup_script = var.startup_script
  metadata = merge(
    {
      enable-osconfig         = "TRUE",
      enable-guest-attributes = "TRUE",
    },
    var.custom_metadata,
  )

  scheduling {
    on_host_maintenance = "MIGRATE"
  }

  disk {
    boot         = true
    source_image = data.google_compute_image.source_image.id
    disk_size_gb = var.root_volume_disk_size_gb
    disk_type    = var.root_volume_disk_type
  }

  disk {
    auto_delete  = true
    boot         = false
    type         = "PERSISTENT"
    disk_size_gb = 500
    disk_type    = "pd-ssd"
  }

  network_interface {
    network = var.network_name

    dynamic "access_config" {
      for_each = var.assign_public_ip_addresses ? ["public_ip"] : []
      content {}
    }
  }

  # For a full list of oAuth 2.0 Scopes, see https://developers.google.com/identity/protocols/googlescopes
  service_account {
    email = var.service_account_email
    scopes = [
      "userinfo-email",
      "compute-ro",
      "https://www.googleapis.com/auth/logging.write",
      "https://www.googleapis.com/auth/monitoring.write",
      "https://www.googleapis.com/auth/trace.append",
      "https://www.googleapis.com/auth/cloud-platform"
    ]
  }

  # Per Terraform Docs (https://www.terraform.io/docs/providers/google/r/compute_instance_template.html#using-with-instance-group-manager),
  # we need to create a new instance template before we can destroy the old one. Note that any Terraform resource on
  # which this Terraform resource depends will also need this lifecycle statement.
  lifecycle {
    create_before_destroy = true

    # TODO: Temporary workaround to avoid unnecessary updates to the instance template.
    #  This should be removed once cluster size is removed from the metadata
    ignore_changes = [metadata]
  }
}
