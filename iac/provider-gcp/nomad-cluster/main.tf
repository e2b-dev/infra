# Server cluster instances are not currently automatically updated when you create a new
# orchestrator image with Packer.
locals {
  nfs_mount_path   = "/orchestrator/shared-store"
  nfs_mount_subdir = "chunks-cache"
  nfs_mount_opts = join(",", [ // for more docs, see https://linux.die.net/man/5/nfs
    "tcp",                     // docs say to avoid it on highspeed connections
    format("nfsvers=%s", var.filestore_cache_enabled ? module.filestore[0].nfs_version == "NFS_V3" ? "3" : "4" : ""),
    "lookupcache=none", // do not cache file handles
    "noac",             // do not use attribute caching
    "noacl",            // do not use an acl
    "nolock",           // do not use locking
  ])

  file_hash = {
    "scripts/run-consul.sh" = substr(filesha256("${path.module}/scripts/run-consul.sh"), 0, 5)
    "scripts/run-nomad.sh"  = substr(filesha256("${path.module}/scripts/run-nomad.sh"), 0, 5)
  }
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
    "scripts/run-nomad.sh"  = "run-nomad",
    "scripts/run-consul.sh" = "run-consul"
  }
}

resource "google_storage_bucket_object" "setup_config_objects" {
  for_each = var.setup_files
  name     = "${each.value}-${local.file_hash[each.key]}.sh"
  source   = "${path.module}/${each.key}"
  bucket   = var.cluster_setup_bucket_name
}

module "network" {
  source = "./network"

  environment = var.environment

  cloudflare_api_token_secret_name = var.cloudflare_api_token_secret_name

  gcp_project_id = var.gcp_project_id

  api_port                  = var.api_port
  docker_reverse_proxy_port = var.docker_reverse_proxy_port
  network_name              = var.network_name
  domain_name               = var.domain_name
  additional_domains        = var.additional_domains

  client_instance_group    = google_compute_region_instance_group_manager.client_pool.instance_group
  client_proxy_port        = var.edge_proxy_port
  client_proxy_health_port = var.edge_api_port

  api_instance_group    = google_compute_instance_group_manager.api_pool.instance_group
  build_instance_group  = google_compute_instance_group_manager.build_pool.instance_group
  server_instance_group = google_compute_instance_group_manager.server_pool.instance_group

  nomad_port = var.nomad_port

  cluster_tag_name = var.cluster_tag_name

  labels = var.labels
  prefix = var.prefix

  additional_api_path_rules = [
    for service in var.additional_api_services : {
      paths      = service.paths
      service_id = service.service_id
    }
  ]

  additional_ports = [for service in var.additional_api_services : service.api_node_group_port]
}

module "filestore" {
  source = "./filestore"

  count = var.filestore_cache_enabled ? 1 : 0

  name         = "${var.prefix}shared-disk-store"
  network_name = var.network_name

  tier        = var.filestore_cache_tier
  capacity_gb = var.filestore_cache_capacity_gb
}
