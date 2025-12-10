locals {
  server_pool_name = "${var.prefix}${var.server_cluster_name}"
  server_startup_script = templatefile("${path.module}/scripts/start-server.sh", {
    NUM_SERVERS                  = var.server_cluster_size
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.file_hash["scripts/run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.file_hash["scripts/run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
  })
}

resource "google_compute_health_check" "server_nomad_check" {
  name                = "${local.server_pool_name}-nomad-check"
  check_interval_sec  = 5
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 10 # 50 seconds

  http_health_check {
    request_path = "/v1/agent/health"
    port         = var.nomad_port
  }
}

resource "google_compute_instance_group_manager" "server_pool" {
  name               = "${local.server_pool_name}-ig"
  base_instance_name = local.server_pool_name

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
    type                    = "PROACTIVE"
    minimal_action          = "REPLACE"
    max_surge_fixed         = null
    max_surge_percent       = null
    max_unavailable_fixed   = 1
    max_unavailable_percent = null
  }

  target_pools = []
  target_size  = var.server_cluster_size

  depends_on = [
    google_compute_instance_template.server,
  ]

  auto_healing_policies {
    health_check      = google_compute_health_check.server_nomad_check.id
    initial_delay_sec = 120
  }

  lifecycle {
    create_before_destroy = false
  }
}

data "google_compute_image" "server_source_image" {
  family = var.server_image_family
}

resource "google_compute_instance_template" "server" {
  name_prefix = "${local.server_pool_name}-"

  instance_description = null
  machine_type         = var.server_machine_type

  tags                    = [var.cluster_tag_name]
  metadata_startup_script = local.server_startup_script
  metadata = {
    enable-osconfig         = "TRUE",
    enable-guest-attributes = "TRUE",
    cluster-size            = var.server_cluster_size,
  }

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
    source_image = data.google_compute_image.server_source_image.self_link
    disk_size_gb = 20
    disk_type    = var.server_boot_disk_type
  }

  network_interface {
    network = var.network_name

    # Create access config dynamically. If a public ip is requested, we just need the empty `access_config` block
    # to automatically assign an external IP address.
    dynamic "access_config" {
      for_each = ["public_ip"]
      content {}
    }
  }

  service_account {
    email = var.google_service_account_email
    scopes = [
      "userinfo-email",
      "compute-ro",
      "https://www.googleapis.com/auth/monitoring.write",
      "https://www.googleapis.com/auth/logging.write",
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

  depends_on = [
    google_storage_bucket_object.setup_config_objects["scripts/run-nomad.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-consul.sh"]
  ]
}
