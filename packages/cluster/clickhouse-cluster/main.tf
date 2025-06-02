resource "google_compute_health_check" "nomad_check" {
  name                = "${var.cluster_name}-nomad-check"
  check_interval_sec  = 15
  timeout_sec         = 10
  healthy_threshold   = 2
  unhealthy_threshold = 10

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
    name              = google_compute_instance_template.server.id
    instance_template = google_compute_instance_template.server.id
  }

  named_port {
    name = var.clickhouse_health_port.name
    port = var.clickhouse_health_port.port
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.nomad_check.id
    initial_delay_sec = 600
  }

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                  = var.environment == "dev" ? "PROACTIVE" : "OPPORTUNISTIC"
    minimal_action        = "REPLACE"  # To prevent having stale data from previous versions of startup script
    max_unavailable_fixed = 1
    replacement_method    = "RECREATE"
  }

  base_instance_name = var.cluster_name
  target_pools       = var.instance_group_target_pools

  depends_on = [
    google_compute_instance_template.server,
  ]
}

resource "google_compute_disk" "stateful_disk" {
  for_each = toset([for i in range(1, var.cluster_size + 1) : tostring(i)])

  name = "${var.cluster_name}-${each.key}-disk"
  type = "pd-ssd"
  zone = var.gcp_zone

  size = 10
}

resource "google_compute_per_instance_config" "instances" {
  for_each = toset([for i in range(1, var.cluster_size + 1) : tostring(i)])

  instance_group_manager = google_compute_instance_group_manager.cluster.name

  name = "${var.cluster_name}-${each.key}"

  preserved_state {
    disk {
      device_name = google_compute_disk.stateful_disk[each.key].name
      mode        = "READ_WRITE"
      delete_rule = var.environment == "dev" ? "ON_PERMANENT_INSTANCE_DELETION" : "NEVER"
      source      = google_compute_disk.stateful_disk[each.key].id
    }

    metadata = {
      "job-constraint" = "${var.job_constraint_prefix}-${each.key}"
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
  )
  tags                    = concat([var.cluster_tag_name], var.custom_tags)
  metadata_startup_script = var.startup_script
  metadata = merge(
    {
      enable-osconfig         = "TRUE",
      enable-guest-attributes = "TRUE",
    },
    {
      node-pool = var.node_pool,
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

    access_config {}
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
  }
}
