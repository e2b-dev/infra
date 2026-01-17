locals {
  api_pool_name = "${var.prefix}orch-api"
  api_additional_ports = [
    for service in var.additional_api_services : {
      name = service.api_node_group_port_name
      port = service.api_node_group_port
    }
  ]

  api_startup_script = templatefile("${path.module}/scripts/start-api.sh", {
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
    NODE_POOL                    = var.api_node_pool
  })
}

resource "google_compute_health_check" "api_nomad_check" {
  name                = "${local.api_pool_name}-nomad-api-check"
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

resource "google_compute_instance_group_manager" "api_pool" {
  name = "${local.api_pool_name}-ig"

  version {
    name              = google_compute_instance_template.api.id
    instance_template = google_compute_instance_template.api.id
  }

  named_port {
    name = var.client_proxy_health_port.name
    port = var.client_proxy_health_port.port
  }

  named_port {
    name = var.client_proxy_port.name
    port = var.client_proxy_port.port
  }

  named_port {
    name = var.api_port.name
    port = var.api_port.port
  }

  named_port {
    name = var.docker_reverse_proxy_port.name
    port = var.docker_reverse_proxy_port.port
  }

  named_port {
    name = var.ingress_port.name
    port = var.ingress_port.port
  }

  dynamic "named_port" {
    for_each = local.api_additional_ports
    content {
      name = "${var.prefix}${named_port.value.name}"
      port = named_port.value.port
    }
  }

  auto_healing_policies {
    health_check      = google_compute_health_check.api_nomad_check.id
    initial_delay_sec = 600
  }

  # Server is a stateful cluster, so the update strategy used to roll out a new GCE Instance Template must be
  # a rolling update.
  update_policy {
    type                    = var.environment == "dev" ? "PROACTIVE" : "OPPORTUNISTIC"
    minimal_action          = "REPLACE"
    max_surge_fixed         = 1
    max_surge_percent       = null
    max_unavailable_fixed   = 1
    max_unavailable_percent = null
    replacement_method      = "SUBSTITUTE"
  }

  base_instance_name = local.api_pool_name
  target_size        = var.api_cluster_size
  target_pools       = []

  depends_on = [
    google_compute_instance_template.api,
  ]
}

data "google_compute_image" "api_source_image" {
  family = var.api_image_family
}

resource "google_compute_instance_template" "api" {
  name_prefix = "${local.api_pool_name}-"

  instance_description = null
  machine_type         = var.api_machine_type

  labels = merge(
    var.labels,
    (var.environment != "dev" ? {
      goog-ops-agent-policy = "v2-x86-template-1-2-0-${var.gcp_zone}"
    } : {})
  )
  tags                    = [var.cluster_tag_name]
  metadata_startup_script = local.api_startup_script
  metadata = merge(
    { api_cluster = "TRUE" },
    {
      enable-osconfig         = "TRUE",
      enable-guest-attributes = "TRUE",
    },
  )

  scheduling {
    on_host_maintenance = "MIGRATE"
  }

  disk {
    boot         = true
    source_image = data.google_compute_image.api_source_image.id
    disk_size_gb = 200
    disk_type    = var.api_boot_disk_type
  }

  network_interface {
    network = var.network_name

    dynamic "access_config" {
      for_each = var.api_use_nat ? [] : ["public_ip"]
      content {}
    }
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

    # TODO: Temporary workaround to avoid unnecessary updates to the instance template.
    #  This should be removed once cluster size is removed from the metadata
    ignore_changes = [metadata]
  }

  depends_on = [
    google_storage_bucket_object.setup_config_objects["scripts/run-nomad.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-consul.sh"]
  ]
}
