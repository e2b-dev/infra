# Server cluster instances are not currently automatically updated when you create a new
# orchestrator image with Packer.

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

variable "targets" {
  type    = list(string)
  default = ["server", "client"]
}

variable "template_files" {
  type = list(string)
  default = [
  "run-nomad.sh", "run-consul.sh"]
}

locals {
  setup_files = setproduct(var.targets, var.template_files)
  files       = { for target in var.targets : target => { for file in var.template_files : file => templatefile("${path.module}/scripts/${file}", { TARGET : target }) } }
  hashes      = { for target in var.targets : target => { for file in var.template_files : file => substr(sha256(local.files[target][file]), 0, 8) } }
}

resource "google_storage_bucket_object" "setup_config_objects" {
  for_each = { for value in local.setup_files : "${value[0]}-${value[1]}" => { target : value[0], path : value[1] } }
  name     = "${split(".", each.value.path)[0]}-${each.value.target}-${local.hashes[each.value.target][each.value.path]}.sh"
  content  = local.files[each.value.target][each.value.path]
  bucket   = var.cluster_setup_bucket_name
}

module "server_cluster" {
  source = "./server"

  startup_script = templatefile("${path.module}/scripts/start-server.sh", {
    NUM_SERVERS                  = var.server_cluster_size
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.hashes["server"]["run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.hashes["server"]["run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
    CONSUL_DNS_REQUEST_TOKEN     = google_secret_manager_secret_version.consul_dns_request_token.secret_data
  })

  cluster_name     = "${var.prefix}${var.server_cluster_name}"
  cluster_size     = var.server_cluster_size
  cluster_tag_name = var.cluster_tag_name

  machine_type = var.server_machine_type
  image_family = var.server_image_family

  network_name          = var.network_name
  service_account_email = var.google_service_account_email

  labels = var.labels

  depends_on = [google_storage_bucket_object.setup_config_objects]
}

module "client_cluster" {
  source = "./client"

  startup_script = templatefile("${path.module}/scripts/start-client.sh", {
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    FC_KERNELS_BUCKET_NAME       = var.fc_kernels_bucket_name
    FC_VERSIONS_BUCKET_NAME      = var.fc_versions_bucket_name
    FC_ENV_PIPELINE_BUCKET_NAME  = var.fc_env_pipeline_bucket_name
    DOCKER_CONTEXTS_BUCKET_NAME  = var.docker_contexts_bucket_name
    DISK_DEVICE_NAME             = var.fc_envs_disk_device_name
    GCP_REGION                   = var.gcp_region
    GOOGLE_SERVICE_ACCOUNT_KEY   = var.google_service_account_key
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.hashes["client"]["run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.hashes["client"]["run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = google_secret_manager_secret_version.consul_gossip_encryption_key.secret_data
    CONSUL_DNS_REQUEST_TOKEN     = google_secret_manager_secret_version.consul_dns_request_token.secret_data
  })

  cluster_name     = "${var.prefix}${var.client_cluster_name}"
  cluster_size     = var.client_cluster_size
  cluster_tag_name = var.cluster_tag_name

  machine_type = var.client_machine_type
  image_family = var.client_image_family

  network_name = var.network_name

  logs_health_proxy_port = var.logs_health_proxy_port
  logs_proxy_port        = var.logs_proxy_port

  client_proxy_port        = var.client_proxy_port
  client_proxy_health_port = var.client_proxy_health_port

  api_port                  = var.api_port
  docker_reverse_proxy_port = var.docker_reverse_proxy_port

  service_account_email = var.google_service_account_email

  fc_envs_disk_name        = var.fc_envs_disk_name
  fc_envs_disk_device_name = var.fc_envs_disk_device_name

  labels     = var.labels
  depends_on = [google_storage_bucket_object.setup_config_objects]
}

module "network" {
  source = "./network"

  gcp_project_id = var.gcp_project_id

  api_port                  = var.api_port
  docker_reverse_proxy_port = var.docker_reverse_proxy_port
  network_name              = var.network_name
  domain_name               = var.domain_name

  client_instance_group    = module.client_cluster.instance_group
  client_proxy_port        = var.client_proxy_port
  client_proxy_health_port = var.client_proxy_health_port

  server_instance_group = module.server_cluster.instance_group

  logs_proxy_port        = var.logs_proxy_port
  logs_health_proxy_port = var.logs_health_proxy_port

  cluster_tag_name = var.cluster_tag_name

  labels = var.labels
  prefix = var.prefix
}
