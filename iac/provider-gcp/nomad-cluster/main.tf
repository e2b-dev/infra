# Server cluster instances are not currently automatically updated when you create a new
# orchestrator image with Packer.
locals {
  build_base_hugepages_percentage  = 60
  client_base_hugepages_percentage = 80

  nfs_mount_path   = "/orchestrator/shared-store"
  nfs_mount_subdir = "chunks-cache"
  nfs_mount_opts = join(",", [ // for more docs, see https://linux.die.net/man/5/nfs
    format("nfsvers=%s", var.filestore_cache_enabled ? module.filestore[0].nfs_version : ""),

    "actimeo=600",          // cache attributes for 600 seconds
    "async",                // delay writes until certain conditions are met
    "hard",                 // retry nfs requests indefinitely until they succeed, never fail
    "lookupcache=positive", // cache successful file handle lookups
    "nconnect=7",           // use multiple connections
    "noacl",                // do not use an acl
    "nocto",                // skip "close-to-open" attribute checks
    "nolock",               // do not use locking
    "noresvport",           // use a non-privileged source port
    "retrans=2",            // retry two times before performing recovery actions
    "rsize=1048576",        // receive 1 MB per read request
    "sec=sys",              // use AUTH_SYS for all requests
    "timeo=600",            // wait 60 seconds (measured in deci-seconds) before retrying a failed request
    "wsize=1048576",        // receive 1 MB per write request
  ])

  file_hash = {
    "scripts/configure-docker-gcp.sh" = substr(filesha256("${path.module}/scripts/configure-docker-gcp.sh"), 0, 5)
    "scripts/run-consul.sh"           = substr(filesha256("${path.module}/scripts/run-consul.sh"), 0, 5)
    "scripts/run-nomad.sh"            = substr(filesha256("${path.module}/scripts/run-nomad.sh"), 0, 5)
  }

  network_hardening_stage_order = {
    disabled = 0
    network  = 1
    server   = 2
    api      = 3
    worker   = 4
    build    = 5
  }
  network_hardening_stage_number = local.network_hardening_stage_order[var.network_hardening_rollout_stage]
  os_login_enabled = {
    server     = local.network_hardening_stage_number >= local.network_hardening_stage_order.server
    api        = local.network_hardening_stage_number >= local.network_hardening_stage_order.api
    client     = local.network_hardening_stage_number >= local.network_hardening_stage_order.worker
    build      = local.network_hardening_stage_number >= local.network_hardening_stage_order.build
    loki       = local.network_hardening_stage_number >= local.network_hardening_stage_order.build
    clickhouse = local.network_hardening_stage_number >= local.network_hardening_stage_order.build
  }
}

# Keep the authorization guard in module.cluster: the normal saved cluster plan
# targets that module, and every replacement path below also depends on it.
resource "terraform_data" "os_login_operator_access_guard" {
  input = var.os_login_operator_access_confirmed

  lifecycle {
    precondition {
      condition = (
        var.network_hardening_rollout_stage == "disabled"
        || (
          var.environment == "dev"
          && var.os_login_operator_access_confirmed
        )
      )
      error_message = "OS Login rollout is restricted to the dev invited-beta fleet and gated on proven operator access: keep the stage disabled outside dev; in dev, grant and prove roles/iap.tunnelResourceAccessor plus roles/compute.osAdminLogin before explicitly confirming the guarded staged workflow."
    }
  }
}

# This sentinel is replaced for every stage and does not complete until the
# stage's administrative firewall and managed group have reached their target
# versions. The persisted marker below therefore remains at the previous stage
# after a template, MIG update, or asynchronous replacement failure, allowing a
# bounded same-stage retry under the reviewed rollout workflow.
resource "terraform_data" "network_hardening_rollout_completion" {
  input = var.network_hardening_rollout_stage
  triggers_replace = [
    var.network_hardening_rollout_stage,
  ]

  lifecycle {
    precondition {
      condition = (
        var.network_hardening_rollout_stage == "disabled"
        || (
          var.environment == "dev"
          && var.os_login_operator_access_confirmed
        )
      )
      error_message = "Network hardening stages are dev-only and require proven IAP and OS Login operator access."
    }
  }

  provisioner "local-exec" {
    command = "\"${abspath("${path.module}/../scripts/wait-network-hardening-stage.sh")}\""
    environment = {
      GCP_PROJECT_ID                  = var.gcp_project_id
      GCP_REGION                      = var.gcp_region
      GCP_ZONE                        = var.gcp_zone
      DOMAIN_NAME                     = var.domain_name
      PREFIX                          = var.prefix
      NETWORK_HARDENING_ROLLOUT_STAGE = var.network_hardening_rollout_stage
      NETWORK_HARDENING_WAIT_SECONDS  = tostring(var.network_hardening_rollout_wait_seconds)
      NETWORK_HARDENING_POLL_SECONDS  = "15"
    }
  }

  depends_on = [
    terraform_data.os_login_operator_access_guard,
    module.network,
    google_compute_region_instance_group_manager.server_pool,
    google_compute_instance_group_manager.api_pool,
    module.client_cluster,
    module.build_cluster,
    google_compute_instance_group_manager.loki_pool,
    google_compute_instance_group_manager.clickhouse_pool,
  ]
}

# The saved-plan assertion verifies an exact one-step transition in this state
# marker, preventing an operator from skipping or reordering fleet stages. It
# is deliberately downstream of the convergence sentinel rather than upstream
# of templates or MIGs.
resource "terraform_data" "network_hardening_rollout_stage" {
  input = var.network_hardening_rollout_stage

  lifecycle {
    precondition {
      condition = (
        var.network_hardening_rollout_stage == "disabled"
        || (
          var.environment == "dev"
          && var.os_login_operator_access_confirmed
        )
      )
      error_message = "Network hardening stages are dev-only and require proven IAP and OS Login operator access."
    }
  }

  depends_on = [terraform_data.network_hardening_rollout_completion]
}

resource "google_secret_manager_secret" "consul_gossip_encryption_key" {
  secret_id = "${var.prefix}consul-gossip-key"

  replication {
    auto {}
  }
}

resource "random_id" "consul_gossip_encryption_key" {
  byte_length = 32
}

resource "google_secret_manager_secret_version" "consul_gossip_encryption_key" {
  secret      = google_secret_manager_secret.consul_gossip_encryption_key.name
  secret_data = random_id.consul_gossip_encryption_key.b64_std
}

resource "google_secret_manager_secret" "consul_dns_request_token" {
  secret_id = "${var.prefix}consul-dns-request-token"

  replication {
    auto {}
  }
}

resource "random_uuid" "consul_dns_request_token" {
}

resource "google_secret_manager_secret_version" "consul_dns_request_token" {
  secret      = google_secret_manager_secret.consul_dns_request_token.name
  secret_data = random_uuid.consul_dns_request_token.result
}

resource "google_project_iam_member" "network_viewer" {
  project = var.gcp_project_id
  member  = "serviceAccount:${var.google_service_account_email}"
  role    = "roles/compute.networkViewer"
}

resource "google_project_iam_member" "monitoring_editor" {
  project = var.gcp_project_id
  member  = "serviceAccount:${var.google_service_account_email}"
  role    = "roles/monitoring.editor"
}
resource "google_project_iam_member" "logging_writer" {
  project = var.gcp_project_id
  member  = "serviceAccount:${var.google_service_account_email}"
  role    = "roles/logging.logWriter"
}

variable "setup_files" {
  type = map(string)
  default = {
    "scripts/configure-docker-gcp.sh" = "configure-docker-gcp",
    "scripts/run-nomad.sh"            = "run-nomad",
    "scripts/run-consul.sh"           = "run-consul"
  }
}

resource "google_storage_bucket_object" "setup_config_objects" {
  for_each        = var.setup_files
  name            = "${each.value}-${local.file_hash[each.key]}.sh"
  source          = "${path.module}/${each.key}"
  bucket          = var.cluster_setup_bucket_name
  deletion_policy = "ABANDON"
}

module "network" {
  source = "./network"

  environment = var.environment

  cloudflare_api_token_secret_name = var.cloudflare_api_token_secret_name

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  api_use_nat              = var.api_use_nat
  api_nat_ips              = var.api_nat_ips
  api_nat_min_ports_per_vm = var.api_nat_min_ports_per_vm

  ingress_port                            = var.ingress_port
  api_port                                = var.api_port
  docker_reverse_proxy_port               = var.docker_reverse_proxy_port
  network_name                            = var.network_name
  domain_name                             = var.domain_name
  additional_domains                      = var.additional_domains
  additional_api_paths_handled_by_ingress = var.additional_api_paths_handled_by_ingress

  client_proxy_port        = var.client_proxy_port
  client_proxy_health_port = var.client_proxy_health_port

  api_instance_group        = google_compute_instance_group_manager.api_pool.instance_group
  extra_api_instance_groups = var.extra_api_instance_groups
  server_instance_group     = google_compute_region_instance_group_manager.server_pool.instance_group

  nomad_port = var.nomad_port

  cluster_tag_name = var.cluster_tag_name

  labels = var.labels
  prefix = var.prefix

  # Consume guard outputs in the two administrative firewall resources. The
  # network child is a legacy provider module and cannot accept depends_on.
  os_login_operator_access_confirmed = terraform_data.os_login_operator_access_guard.output
  network_hardening_rollout_stage    = var.network_hardening_rollout_stage
}

module "filestore" {
  source = "./filestore"

  count = var.filestore_cache_enabled ? 1 : 0

  name         = "${var.prefix}shared-disk-store"
  network_name = var.network_name

  tier        = var.filestore_cache_tier
  capacity_gb = var.filestore_cache_capacity_gb
  nfs_version = var.filestore_nfs_version
}


module "build_cluster" {
  for_each = var.build_clusters_config
  source   = "./worker-cluster"

  gcp_project_id               = var.gcp_project_id
  gcp_region                   = var.gcp_region
  gcp_zone                     = var.gcp_zone
  google_service_account_email = var.google_service_account_email

  cluster_size     = each.value.cluster_size
  cache_disks      = each.value.cache_disks
  machine_type     = each.value.machine.type
  min_cpu_platform = each.value.machine.min_cpu_platform
  boot_disk        = each.value.boot_disk
  autoscaler       = each.value.autoscaler

  cluster_name              = "${var.prefix}${var.build_cluster_name}-${each.key}"
  image_family              = var.build_image_family
  network_name              = var.network_name
  base_hugepages_percentage = coalesce((each.value.hugepages_percentage), local.build_base_hugepages_percentage)
  network_interface_type    = each.value.network_interface_type
  node_labels               = each.value.node_labels
  use_cloud_nat             = var.api_use_nat

  cluster_tag_name                         = var.cluster_tag_name
  node_pool                                = var.build_node_pool
  nomad_port                               = var.nomad_port
  consul_acl_token_secret                  = var.consul_acl_token_secret
  nomad_acl_token_secret                   = var.nomad_acl_token_secret
  consul_gossip_encryption_key_secret_data = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
  consul_dns_request_token_secret_data     = google_secret_manager_secret_version.consul_dns_request_token.secret_data

  docker_contexts_bucket_name = var.docker_contexts_bucket_name
  cluster_setup_bucket_name   = var.cluster_setup_bucket_name
  fc_env_pipeline_bucket_name = var.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name      = var.fc_kernels_bucket_name
  fc_versions_bucket_name     = var.fc_versions_bucket_name
  fc_busybox_bucket_name      = var.fc_busybox_bucket_name

  filestore_cache_enabled = var.filestore_cache_enabled
  nfs_ip_addresses        = var.filestore_cache_enabled ? module.filestore[0].nfs_ip_addresses : []
  nfs_mount_path          = local.nfs_mount_path
  nfs_mount_subdir        = local.nfs_mount_subdir
  nfs_mount_opts          = local.nfs_mount_opts
  persistent_volume_types = {} // don't need to access persistent volumes when building templates

  environment = var.environment
  labels      = var.labels

  file_hash = local.file_hash

  set_orchestrator_version_metadata  = false
  enable_os_login                    = local.os_login_enabled.build
  os_login_operator_access_confirmed = terraform_data.os_login_operator_access_guard.output

  depends_on = [
    google_storage_bucket_object.setup_config_objects["scripts/configure-docker-gcp.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-nomad.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-consul.sh"]
  ]
}

module "client_cluster" {
  for_each = var.client_clusters_config
  source   = "./worker-cluster"

  gcp_project_id               = var.gcp_project_id
  gcp_region                   = var.gcp_region
  gcp_zone                     = var.gcp_zone
  google_service_account_email = var.google_service_account_email

  cluster_size     = each.value.cluster_size
  cache_disks      = each.value.cache_disks
  machine_type     = each.value.machine.type
  min_cpu_platform = each.value.machine.min_cpu_platform
  boot_disk        = each.value.boot_disk
  autoscaler       = each.value.autoscaler

  workload_autoscaler_shadow_enabled = each.key == "default" && var.monad_worker_autoscaler_shadow_enabled
  enable_os_login                    = local.os_login_enabled.client
  os_login_operator_access_confirmed = terraform_data.os_login_operator_access_guard.output

  // This is here for backwards compatibility
  cluster_name              = each.key == "default" ? "${var.prefix}${var.client_cluster_name}" : "${var.prefix}${var.client_cluster_name}-${each.key}"
  image_family              = var.client_image_family
  network_name              = var.network_name
  base_hugepages_percentage = coalesce((each.value.hugepages_percentage), local.client_base_hugepages_percentage)
  network_interface_type    = each.value.network_interface_type
  node_labels               = each.value.node_labels
  use_cloud_nat             = var.api_use_nat

  cluster_tag_name                         = var.cluster_tag_name
  node_pool                                = var.orchestrator_node_pool
  nomad_port                               = var.nomad_port
  consul_acl_token_secret                  = var.consul_acl_token_secret
  nomad_acl_token_secret                   = var.nomad_acl_token_secret
  consul_gossip_encryption_key_secret_data = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
  consul_dns_request_token_secret_data     = google_secret_manager_secret_version.consul_dns_request_token.secret_data

  docker_contexts_bucket_name = var.docker_contexts_bucket_name
  cluster_setup_bucket_name   = var.cluster_setup_bucket_name
  fc_env_pipeline_bucket_name = var.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name      = var.fc_kernels_bucket_name
  fc_versions_bucket_name     = var.fc_versions_bucket_name
  fc_busybox_bucket_name      = var.fc_busybox_bucket_name

  filestore_cache_enabled = var.filestore_cache_enabled
  nfs_ip_addresses        = var.filestore_cache_enabled ? module.filestore[0].nfs_ip_addresses : []
  nfs_mount_path          = local.nfs_mount_path
  nfs_mount_subdir        = local.nfs_mount_subdir
  nfs_mount_opts          = local.nfs_mount_opts
  persistent_volume_types = var.persistent_volume_types

  environment = var.environment
  labels      = var.labels

  file_hash = local.file_hash

  # The dev orchestrator job has the stable ID "dev" and no version metadata
  # constraint. Avoid waiting during cluster bootstrap for the phase-two Nomad
  # variable; non-dev keeps version-pinned worker scheduling.
  set_orchestrator_version_metadata = var.environment != "dev"

  depends_on = [
    google_storage_bucket_object.setup_config_objects["scripts/configure-docker-gcp.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-nomad.sh"],
    google_storage_bucket_object.setup_config_objects["scripts/run-consul.sh"]
  ]
}
