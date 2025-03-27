locals {
  instance_group_update_policy_max_surge_fixed = try(var.instance_group_update_policy_max_surge_fixed, var.cluster_size - 1 ? 1 : 0)
}
resource "google_compute_health_check" "nomad_check" {
  name                = "${var.cluster_name}-nomad-check"
  check_interval_sec  = 5
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 10 # 50 seconds

  http_health_check {
    request_path = "/v1/agent/health"
    port         = var.nomad_port
  }
}

resource "google_compute_instance_group_manager" "server_cluster" {
  name               = "${var.cluster_name}-ig"
  base_instance_name = var.cluster_name

  version {
    instance_template = google_compute_instance_template.server.id
  }

  named_port {
    name = "nomad"
    port = var.nomad_port
  }

  named_port {
    name = "consul"
    port = 8500
  }

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                    = var.instance_group_update_policy_type
    minimal_action          = var.instance_group_update_policy_minimal_action
    max_surge_fixed         = local.instance_group_update_policy_max_surge_fixed
    max_surge_percent       = var.instance_group_update_policy_max_surge_percent
    max_unavailable_fixed   = var.instance_group_update_policy_max_unavailable_fixed
    max_unavailable_percent = var.instance_group_update_policy_max_unavailable_percent
  }

  target_pools = var.instance_group_target_pools
  target_size  = var.cluster_size

  depends_on = [
    google_compute_instance_template.server,
  ]

  auto_healing_policies {
    health_check      = google_compute_health_check.nomad_check.id
    initial_delay_sec = 120
  }

  lifecycle {
    create_before_destroy = false
  }
}

data "google_compute_image" "source_image" {
  family = var.image_family
}

resource "google_compute_instance_template" "server" {
  name_prefix = "${var.cluster_name}-"

  instance_description = var.cluster_description
  machine_type         = var.machine_type

  tags                    = concat([var.cluster_tag_name], var.custom_tags)
  metadata_startup_script = var.startup_script
  metadata = merge(
    {
      enable-osconfig                          = "TRUE",
      enable-guest-attributes                  = "TRUE",
      (var.metadata_key_name_for_cluster_size) = var.cluster_size,
    },
    var.custom_metadata,
  )

  labels = merge(
    var.labels,
    (var.environment != "dev" ? {
      goog-ops-agent-policy = "v2-x86-template-1-2-0-${var.gcp_zone}"
    } : {})
  )
  scheduling {
    on_host_maintenance = "MIGRATE"
  }

  disk {
    boot         = true
    source_image = data.google_compute_image.source_image.self_link
    disk_size_gb = var.root_volume_disk_size_gb
    disk_type    = var.root_volume_disk_type
  }

  network_interface {
    network = var.network_name

    # Create access config dynamically. If a public ip is requested, we just need the empty `access_config` block
    # to automatically assign an external IP address.
    dynamic "access_config" {
      for_each = var.assign_public_ip_addresses ? ["public_ip"] : []
      content {}
    }
  }

  service_account {
    email = var.service_account_email
    scopes = concat(
      [
        "userinfo-email",
        "compute-ro",
        "https://www.googleapis.com/auth/monitoring.write",
        "https://www.googleapis.com/auth/logging.write",
        "https://www.googleapis.com/auth/trace.append",
        "https://www.googleapis.com/auth/cloud-platform"
      ],
      var.service_account_scopes,
    )
  }

  # Per Terraform Docs (https://www.terraform.io/docs/providers/google/r/compute_instance_template.html#using-with-instance-group-manager),
  # we need to create a new instance template before we can destroy the old one. Note that any Terraform resource on
  # which this Terraform resource depends will also need this lifecycle statement.
  lifecycle {
    create_before_destroy = true
  }
}
