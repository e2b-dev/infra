resource "google_compute_health_check" "nomad_check" {
  name                = "${var.cluster_name}-nomad-check"
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

resource "google_compute_instance_group_manager" "cluster" {
  name = "${var.cluster_name}-ig"

  version {
    name              = google_compute_instance_template.cluster.id
    instance_template = google_compute_instance_template.cluster.id
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.nomad_check.id
    initial_delay_sec = 600
  }

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                  = "PROACTIVE"
    minimal_action        = "REFRESH"
    max_unavailable_fixed = 1
    replacement_method    = "RECREATE"
  }

  base_instance_name = var.cluster_name
  target_size        = var.cluster_size
  target_pools       = var.instance_group_target_pools

  depends_on = [
    google_compute_instance_template.server,
  ]
}

resource "google_compute_per_instance_config" "instances" {
  for_each = toset(range(1, var.cluster_size + 1))

  instance_group_manager = google_compute_instance_group_manager.cluster.name
  zone                   = google_compute_instance_group_manager.cluster.zone

  name = "${var.cluster_name}-${each.key}"

  preserved_state {
    disk {
      device_name = "${var.cluster_name}-${each.key}-disk"
      mode        = "READ_WRITE"
      source      = "${var.cluster_name}-${each.key}-disk"
      delete_rule = "ON_PERMANENT_INSTANCE_DELETION"
    }

    metadata = {
      "${var.index_attribute}" = each.key
    }
  }
}

data "google_compute_image" "source_image" {
  family = var.image_family
}

resource "google_compute_instance_template" "server" {
  name_prefix = "${var.cluster_name}-"

  instance_description = var.cluster_description
  machine_type         = var.machine_type

  labels = merge(
    var.labels,
    (var.environment != "dev" ? {
      goog-ops-agent-policy = "v2-x86-template-1-2-0-${var.gcp_zone}"
    } : {})
  )
  tags                    = concat([var.cluster_tag_name], var.custom_tags)
  metadata_startup_script = var.startup_script
  metadata = merge(
    { monitoring_server_cluster = "TRUE" },
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
