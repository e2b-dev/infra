terraform {
  required_version = ">= 1.5.0, < 1.6.0"

  backend "gcs" {
    prefix = "terraform/orchestration/state"
  }

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "6.50.0"
    }

    google-beta = {
      source  = "hashicorp/google-beta"
      version = "6.50.0"
    }

    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.52.5"
    }

    nomad = {
      source  = "hashicorp/nomad"
      version = "2.1.0"
    }

    random = {
      source  = "hashicorp/random"
      version = "3.8.1"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

provider "google-beta" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

data "google_secret_manager_secret_version" "routing_domains" {
  secret = module.init.routing_domains_secret_name
}

locals {
  additional_domains = nonsensitive(jsondecode(data.google_secret_manager_secret_version.routing_domains.secret_data))

  # Normalize additional_api_paths_handled_by_ingress to support both legacy (list of strings)
  # and new (list of objects) formats. Strings are converted to objects with paths = [string].
  normalized_api_paths_handled_by_ingress = [
    for item in var.additional_api_paths_handled_by_ingress : {
      paths       = try(item.paths, [item])
      timeout_sec = try(item.timeout_sec, null)
    }
  ]

  // Check if all clusters has size greater than 1
  template_manages_clusters_size_gt_1 = alltrue([for c in var.build_clusters_config : (c.cluster_size > 1)])

  // for more docs, see https://linux.die.net/man/5/nfs
  default_persistent_volume_type_nfs_mount_options = [
    // network
    "hard",       // retry nfs requests indefinitely until they succeed, never fail
    "async",      // write eventually
    "nconnect=7", // use multiple connections
    "noresvport", // use a non-privileged source port
    "retrans=3",  // retry two times before performing recovery actions
    "timeo=600",  // wait 60 seconds (measured in deci-seconds) before retrying a failed request

    // resiliency
    "fg",              // wait for mounts to finish before exiting
    "cto",             // enable "close-to-open" attribute checks
    "lock",            // enable network locking
    "local_lock=none", // all locks are network locks

    // caching
    "noac",             // disable attribute caching. slower, but more reliable
    "lookupcache=none", // disable lookup caching

    // security
    "noacl",   // do not use an acl
    "sec=sys", // use AUTH_SYS for all requests
  ]

  persistent_volume_types = {
    for key, config in var.persistent_volume_types : key => {
      local_mount_path = "/mnt/persistent-volume-types/${key}"
      nfs_location     = module.persistent-volume-types[key].nfs_location
      nfs_mount_opts = join(",", concat(
        [format("nfsvers=%s", module.persistent-volume-types[key].nfs_version)],
        config.mount_options != null ? config.mount_options : local.default_persistent_volume_type_nfs_mount_options,
      ))
    }
  }
}

module "init" {
  source = "./init"

  labels        = var.labels
  prefix        = var.prefix
  bucket_prefix = var.bucket_prefix

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  template_bucket_location = var.template_bucket_location
  template_bucket_name     = var.template_bucket_name

  anywhere_cache = {
    enabled          = var.anywhere_cache_enabled
    admission_policy = var.anywhere_cache_admission_policy
    ttl              = var.anywhere_cache_ttl
  }
}

module "cluster" {
  source = "./nomad-cluster"

  environment = var.environment

  cloudflare_api_token_secret_name = module.init.cloudflare_api_token_secret_name
  gcp_project_id                   = var.gcp_project_id
  gcp_region                       = var.gcp_region
  gcp_zone                         = var.gcp_zone
  google_service_account_key       = module.init.google_service_account_key
  network_name                     = var.network_name

  build_clusters_config  = var.build_clusters_config
  client_clusters_config = var.client_clusters_config

  api_cluster_size        = var.api_cluster_size
  clickhouse_cluster_size = var.clickhouse_cluster_size
  server_cluster_size     = var.server_cluster_size
  loki_cluster_size       = var.loki_cluster_size

  server_machine_type     = var.server_machine_type
  api_machine_type        = var.api_machine_type
  clickhouse_machine_type = var.clickhouse_machine_type
  loki_machine_type       = var.loki_machine_type

  api_node_pool          = var.api_node_pool
  build_node_pool        = var.build_node_pool
  clickhouse_node_pool   = var.clickhouse_node_pool
  loki_node_pool         = var.loki_node_pool
  orchestrator_node_pool = var.orchestrator_node_pool

  api_use_nat              = var.api_use_nat
  api_nat_ips              = var.api_nat_ips
  api_nat_min_ports_per_vm = var.api_nat_min_ports_per_vm

  client_proxy_port        = var.client_proxy_port
  client_proxy_health_port = var.client_proxy_health_port

  ingress_port                 = var.ingress_port
  api_port                     = var.api_port
  docker_reverse_proxy_port    = var.docker_reverse_proxy_port
  nomad_port                   = var.nomad_port
  google_service_account_email = module.init.service_account_email
  domain_name                  = var.domain_name
  ingress_timeout_seconds      = var.ingress_timeout_seconds

  additional_domains                      = local.additional_domains
  additional_api_paths_handled_by_ingress = local.normalized_api_paths_handled_by_ingress

  docker_contexts_bucket_name = module.init.envs_docker_context_bucket_name
  cluster_setup_bucket_name   = module.init.cluster_setup_bucket_name
  fc_env_pipeline_bucket_name = module.init.fc_env_pipeline_bucket_name
  fc_kernels_bucket_name      = module.init.fc_kernels_bucket_name
  fc_versions_bucket_name     = module.init.fc_versions_bucket_name
  fc_busybox_bucket_name      = module.init.fc_busybox_bucket_name

  clickhouse_job_constraint_prefix = var.clickhouse_job_constraint_prefix
  clickhouse_health_port           = var.clickhouse_health_port

  consul_acl_token_secret = module.init.consul_acl_token_secret
  nomad_acl_token_secret  = module.init.nomad_acl_token_secret

  filestore_cache_enabled     = var.filestore_cache_enabled
  filestore_cache_tier        = var.filestore_cache_tier
  filestore_cache_capacity_gb = var.filestore_cache_capacity_gb
  filestore_nfs_version       = var.filestore_nfs_version

  persistent_volume_types = local.persistent_volume_types

  labels = var.labels
  prefix = var.prefix

  # Boot disks
  api_boot_disk_type        = var.api_boot_disk_type
  server_boot_disk_type     = var.server_boot_disk_type
  server_boot_disk_size_gb  = var.server_boot_disk_size_gb
  clickhouse_boot_disk_type = var.clickhouse_boot_disk_type
  loki_boot_disk_type       = var.loki_boot_disk_type

  # ClickHouse stateful data disk
  clickhouse_stateful_disk_type    = var.clickhouse_stateful_disk_type
  clickhouse_stateful_disk_size_gb = var.clickhouse_stateful_disk_size_gb
}

moved {
  from = module.nomad
  to   = module.nomad[0]
}

module "nomad" {
  source = "./nomad"

  count = var.include_nomad ? 1 : 0

  infra_config = local.nomad_config
}


module "redis" {
  source = "./redis"
  count  = var.redis_managed ? 1 : 0

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  gcp_zone       = var.gcp_zone
  network_name   = var.network_name

  redis_cluster_url_secret_version   = module.init.redis_cluster_url_secret_version
  redis_tls_ca_base64_secret_version = module.init.redis_tls_ca_base64_secret_version

  shard_count    = var.redis_shard_count
  engine_version = var.gcp_redis_engine_version
  node_type      = var.gcp_redis_node_type
  prefix         = var.prefix
}

module "remote_repository" {
  source = "./remote-repository"

  count = var.remote_repository_enabled ? 1 : 0

  depends_on = [module.init]

  prefix = var.prefix

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region

  google_service_account_email = module.init.service_account_email

  dockerhub_username_secret_name = module.init.dockerhub_username_secret_name
  dockerhub_password_secret_name = module.init.dockerhub_password_secret_name
}
