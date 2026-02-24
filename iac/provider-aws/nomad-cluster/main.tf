locals {
  nfs_mount_path   = var.efs_mount_path
  nfs_mount_subdir = "chunks-cache"

  file_hash = {
    "scripts/run-consul.sh" = substr(filesha256("${path.module}/scripts/run-consul.sh"), 0, 5)
    "scripts/run-nomad.sh"  = substr(filesha256("${path.module}/scripts/run-nomad.sh"), 0, 5)
  }
}

# Consul gossip encryption key
resource "aws_secretsmanager_secret" "consul_gossip_encryption_key" {
  name = "${var.prefix}consul-gossip-key"
  tags = var.tags
}

resource "random_id" "consul_gossip_encryption_key" {
  byte_length = 32
}

resource "aws_secretsmanager_secret_version" "consul_gossip_encryption_key" {
  secret_id     = aws_secretsmanager_secret.consul_gossip_encryption_key.id
  secret_string = random_id.consul_gossip_encryption_key.b64_std
}

# Consul DNS request token
resource "aws_secretsmanager_secret" "consul_dns_request_token" {
  name = "${var.prefix}consul-dns-request-token"
  tags = var.tags
}

resource "random_uuid" "consul_dns_request_token" {}

resource "aws_secretsmanager_secret_version" "consul_dns_request_token" {
  secret_id     = aws_secretsmanager_secret.consul_dns_request_token.id
  secret_string = random_uuid.consul_dns_request_token.result
}

# Upload startup scripts to S3
variable "setup_files" {
  type = map(string)
  default = {
    "scripts/run-nomad.sh"  = "run-nomad"
    "scripts/run-consul.sh" = "run-consul"
  }
}

resource "aws_s3_object" "setup_config_objects" {
  for_each = var.setup_files

  bucket = var.cluster_setup_bucket_name
  key    = "${each.value}-${local.file_hash[each.key]}.sh"
  source = "${path.module}/${each.key}"
  etag   = filemd5("${path.module}/${each.key}")
}

# Build clusters
module "build_cluster" {
  for_each = var.build_clusters_config
  source   = "./worker-cluster"

  cluster_name = "${var.prefix}orch-build-${each.key}"

  aws_region         = var.aws_region
  availability_zones = var.availability_zones
  subnet_ids         = var.public_subnet_ids
  security_group_ids = [var.cluster_sg_id]

  iam_instance_profile_name = var.iam_instance_profile_name

  instance_type         = each.value.instance_type
  nested_virtualization = each.value.nested_virtualization
  ami_id                = var.ami_id
  cluster_size          = each.value.cluster_size
  boot_disk_size_gb     = each.value.boot_disk_size_gb
  boot_disk_type        = each.value.boot_disk_type
  cache_disk_size_gb    = each.value.cache_disk_size_gb
  autoscaler            = each.value.autoscaler

  hugepages_percentage = coalesce(each.value.hugepages_percentage, 60)

  consul_acl_token_secret                  = var.consul_acl_token_secret
  nomad_acl_token_secret                   = var.nomad_acl_token_secret
  consul_gossip_encryption_key_secret_data = random_id.consul_gossip_encryption_key.b64_std
  consul_dns_request_token_secret_data     = random_uuid.consul_dns_request_token.result

  cluster_setup_bucket_name   = var.cluster_setup_bucket_name
  fc_kernels_bucket_name      = var.fc_kernels_bucket_name
  fc_versions_bucket_name     = var.fc_versions_bucket_name
  fc_env_pipeline_bucket_name = var.fc_env_pipeline_bucket_name
  docker_contexts_bucket_name = var.docker_contexts_bucket_name

  nomad_port = var.nomad_port
  node_pool  = "build"

  efs_cache_enabled = var.efs_cache_enabled
  efs_dns_name      = var.efs_dns_name
  efs_mount_path    = local.nfs_mount_path
  efs_mount_subdir  = local.nfs_mount_subdir

  environment = var.environment
  prefix      = var.prefix
  tags        = var.tags

  file_hash = local.file_hash

  depends_on = [
    aws_s3_object.setup_config_objects
  ]
}

# Client clusters
module "client_cluster" {
  for_each = var.client_clusters_config
  source   = "./worker-cluster"

  cluster_name = each.key == "default" ? "${var.prefix}orch-client" : "${var.prefix}orch-client-${each.key}"

  aws_region         = var.aws_region
  availability_zones = var.availability_zones
  subnet_ids         = var.public_subnet_ids
  security_group_ids = [var.cluster_sg_id]

  iam_instance_profile_name = var.iam_instance_profile_name

  instance_type         = each.value.instance_type
  nested_virtualization = each.value.nested_virtualization
  ami_id                = var.ami_id
  cluster_size          = each.value.cluster_size
  boot_disk_size_gb     = each.value.boot_disk_size_gb
  boot_disk_type        = each.value.boot_disk_type
  cache_disk_size_gb    = each.value.cache_disk_size_gb
  autoscaler            = each.value.autoscaler

  hugepages_percentage = coalesce(each.value.hugepages_percentage, 80)

  consul_acl_token_secret                  = var.consul_acl_token_secret
  nomad_acl_token_secret                   = var.nomad_acl_token_secret
  consul_gossip_encryption_key_secret_data = random_id.consul_gossip_encryption_key.b64_std
  consul_dns_request_token_secret_data     = random_uuid.consul_dns_request_token.result

  cluster_setup_bucket_name   = var.cluster_setup_bucket_name
  fc_kernels_bucket_name      = var.fc_kernels_bucket_name
  fc_versions_bucket_name     = var.fc_versions_bucket_name
  fc_env_pipeline_bucket_name = var.fc_env_pipeline_bucket_name
  docker_contexts_bucket_name = var.docker_contexts_bucket_name

  nomad_port = var.nomad_port
  node_pool  = "default"

  efs_cache_enabled = var.efs_cache_enabled
  efs_dns_name      = var.efs_dns_name
  efs_mount_path    = local.nfs_mount_path
  efs_mount_subdir  = local.nfs_mount_subdir

  environment = var.environment
  prefix      = var.prefix
  tags        = var.tags

  file_hash = local.file_hash

  depends_on = [
    aws_s3_object.setup_config_objects
  ]
}
