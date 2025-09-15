locals {
  clickhouse_pool_name = "${var.prefix}${var.clickhouse_cluster_name}"
  clickhouse_start_script = templatefile("${path.module}/scripts/start-clickhouse.sh", {
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    FC_KERNELS_BUCKET_NAME       = var.fc_kernels_bucket_name
    FC_VERSIONS_BUCKET_NAME      = var.fc_versions_bucket_name
    FC_ENV_PIPELINE_BUCKET_NAME  = var.fc_env_pipeline_bucket_name
    DOCKER_CONTEXTS_BUCKET_NAME  = var.docker_contexts_bucket_name
    GCP_REGION                   = var.gcp_region
    GOOGLE_SERVICE_ACCOUNT_KEY   = var.google_service_account_key
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.file_hash["scripts/run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.file_hash["scripts/run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
    CONSUL_DNS_REQUEST_TOKEN     = google_secret_manager_secret_version.consul_dns_request_token.secret_data
    NODE_POOL                    = var.clickhouse_node_pool
  })
}

resource "google_compute_health_check" "clickhouse_nomad_check" {
  name                = "${local.clickhouse_pool_name}-nomad-check"
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

resource "google_compute_instance_group_manager" "clickhouse_pool" {
  name = "${local.clickhouse_pool_name}-ig"

  version {
    name              = google_compute_instance_template.clickhouse.id
    instance_template = google_compute_instance_template.clickhouse.id
  }

  named_port {
    name = var.clickhouse_health_port.name
    port = var.clickhouse_health_port.port
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.clickhouse_nomad_check.id
    initial_delay_sec = 600
  }

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                  = var.environment == "dev" ? "PROACTIVE" : "OPPORTUNISTIC"
    minimal_action        = "REPLACE" # To prevent having stale data from previous versions of startup script
    max_unavailable_fixed = 1
    replacement_method    = "RECREATE"
  }

  base_instance_name = local.clickhouse_pool_name
  target_pools       = []

  depends_on = [
    google_compute_instance_template.clickhouse,
  ]
}

resource "google_compute_disk" "clickhouse_stateful_disk" {
  for_each = toset([for i in range(1, var.clickhouse_cluster_size + 1) : tostring(i)])

  name = "${local.clickhouse_pool_name}-${each.key}-disk"
  type = "pd-ssd"
  zone = var.gcp_zone

  size = 100

  lifecycle {
    ignore_changes = [size]
  }
}

resource "google_compute_per_instance_config" "clickhouse_instances" {
  for_each = toset([for i in range(1, var.clickhouse_cluster_size + 1) : tostring(i)])

  instance_group_manager = google_compute_instance_group_manager.clickhouse_pool.name

  name = "${local.clickhouse_pool_name}-${each.key}"

  preserved_state {
    disk {
      device_name = google_compute_disk.clickhouse_stateful_disk[each.key].name
      mode        = "READ_WRITE"
      delete_rule = var.environment == "dev" ? "ON_PERMANENT_INSTANCE_DELETION" : "NEVER"
      source      = google_compute_disk.clickhouse_stateful_disk[each.key].id
    }

    metadata = {
      "job-constraint" = "${var.clickhouse_job_constraint_prefix}-${each.key}"
    }
  }
}

data "google_compute_image" "clickhouse_source_image" {
  family = var.api_image_family
}

resource "google_compute_instance_template" "clickhouse" {
  name_prefix = "${local.clickhouse_pool_name}-"

  instance_description = null
  machine_type         = var.clickhouse_machine_type

  labels = merge(
    var.labels,
  )
  tags                    = [var.cluster_tag_name]
  metadata_startup_script = local.clickhouse_start_script
  metadata = {
    enable-osconfig         = "TRUE",
    enable-guest-attributes = "TRUE",
    node-pool               = var.clickhouse_node_pool,
  }

  scheduling {
    on_host_maintenance = "MIGRATE"
  }

  disk {
    boot         = true
    source_image = data.google_compute_image.clickhouse_source_image.id
    disk_size_gb = 200
    disk_type    = "pd-ssd"
  }

  network_interface {
    network = var.network_name

    access_config {}
  }

  # For a full list of oAuth 2.0 Scopes, see https://developers.google.com/identity/protocols/googlescopes
  service_account {
    email = var.google_service_account_email
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

  depends_on = [
    google_storage_bucket_object.setup_config_objects
  ]
}
