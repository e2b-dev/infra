locals {
  setup_files = {
    "scripts/run-consul.sh" = "run-consul",
    "scripts/run-nomad.sh"  = "run-nomad"
  }

  setup_files_hash = {
    "run-consul" = substr(filesha256("${path.module}/scripts/run-consul.sh"), 0, 5)
    "run-nomad"  = substr(filesha256("${path.module}/scripts/run-nomad.sh"), 0, 5)
  }

  // The tag name the Compute Instances will look for to automatically discover each other and form a cluster.
  cluster_tag_name  = "cluster-discovery-name"
  cluster_tag_value = "${var.prefix}nomad-cluster"

  aws_ecr_account_repository_domain = "${var.aws_account_id}.dkr.ecr.${var.aws_region}.amazonaws.com"
}

data "aws_region" "current" {}

data "aws_caller_identity" "current" {}

data "aws_s3_bucket" "setup" {
  bucket = var.setup_bucket_name
}

data "aws_s3_bucket" "fc_env_pipeline" {
  bucket = var.fc_env_pipeline_bucket_name
}

data "aws_s3_bucket" "fc_kernels" {
  bucket = var.fc_kernels_bucket_name
}

data "aws_s3_bucket" "fc_versions" {
  bucket = var.fc_versions_bucket_name
}

data "aws_s3_bucket" "templates_build_cache_bucket" {
  bucket = var.templates_build_cache_bucket_name
}

data "aws_s3_bucket" "templates_bucket" {
  bucket = var.templates_bucket_name
}

data "aws_s3_bucket" "loki_bucket" {
  bucket = var.loki_bucket_name
}

data "aws_s3_bucket" "clickhouse_bucket" {
  bucket = var.clickhouse_backups_bucket_name
}

resource "aws_s3_object" "setup_config" {
  for_each = local.setup_files
  bucket   = var.setup_bucket_name
  key      = "${each.value}-${local.setup_files_hash[each.value]}.sh"
  source   = "${path.module}/${each.key}"
}

resource "aws_iam_policy" "cluster_node_policy" {
  name   = "${var.prefix}cluster-node-policy"
  policy = data.aws_iam_policy_document.cluster_node_policy.json
}

data "aws_ecr_repository" "custom_environments" {
  name = var.custom_environments_repository_name
}

data "aws_iam_policy_document" "cluster_node_policy" {
  // Allow read access to S3 bucket with Consul and Nomad setup scripts
  statement {
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:GetObject"
    ]
    resources = [
      "${data.aws_s3_bucket.setup.arn}/*",
      data.aws_s3_bucket.setup.arn,
    ]
  }

  statement {
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchGetImage",
      "ecr:BatchDeleteImage",
      "ecr:DescribeRepositories",
      "ecr:InitiateLayerUpload",
      "ecr:UploadLayerPart",
      "ecr:CompleteLayerUpload",
      "ecr:PutImage"
    ]
    resources = [
      data.aws_ecr_repository.custom_environments.arn
    ]
  }

  // Allow read access from binaries buckets
  statement {
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:GetBucketLocation",
      "s3:GetObject",
      "s3:GetObjectVersion",
    ]
    resources = [
      "${data.aws_s3_bucket.fc_env_pipeline.arn}/*",
      data.aws_s3_bucket.fc_env_pipeline.arn,

      "${data.aws_s3_bucket.fc_kernels.arn}/*",
      data.aws_s3_bucket.fc_kernels.arn,

      "${data.aws_s3_bucket.fc_versions.arn}/*",
      data.aws_s3_bucket.fc_versions.arn,
    ]
  }

  statement {
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
    ]
    resources = [
      // Templates
      "${data.aws_s3_bucket.templates_bucket.arn}/*",
      data.aws_s3_bucket.templates_bucket.arn,

      // Template build cache
      "${data.aws_s3_bucket.templates_build_cache_bucket.arn}/*",
      data.aws_s3_bucket.templates_build_cache_bucket.arn,
    ]
  }

  // Allow EC2 describe instances for cluster node discovery
  statement {
    effect = "Allow"
    actions = [
      "ec2:DescribeInstances",
      "ec2:DescribeTags"
    ]
    resources = ["*"]
  }

  // Allow using Docker ECR repositories with Nomad
  // Nomad handles authentication and image pulling internally
  statement {
    effect = "Allow"
    actions = [
      "ecr:GetAuthorizationToken",
      "ecr:BatchCheckLayerAvailability",
      "ecr:GetDownloadUrlForLayer",
      "ecr:GetRepositoryPolicy",
      "ecr:DescribeRepositories",
      "ecr:ListImages",
      "ecr:DescribeImages",
      "ecr:BatchGetImage",
      "ecr:GetLifecyclePolicy",
      "ecr:GetLifecyclePolicyPreview",
      "ecr:ListTagsForResource",
      "ecr:DescribeImageScanFindings"
    ]
    resources = ["*"]
  }
}

data "aws_iam_policy_document" "cluster_node_ec2_policy" {
  statement {
    actions = ["sts:AssumeRole", "sts:TagSession"]
    effect  = "Allow"

    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

// ---
// Nodepool Modules
// ---

module "control_server" {
  source = "../modules/nodepool-control-server"

  prefix         = var.prefix
  aws_account_id = var.aws_account_id

  cluster_tag_name  = local.cluster_tag_name
  cluster_tag_value = local.cluster_tag_value

  cluster_node_policy_arn      = aws_iam_policy.cluster_node_policy.arn
  cluster_node_ec2_policy_json = data.aws_iam_policy_document.cluster_node_ec2_policy.json

  setup_bucket_name = var.setup_bucket_name
  setup_files_hash  = local.setup_files_hash

  security_group_ids  = var.control_server_security_group_ids
  target_group_arns   = var.control_server_target_group_arns
  vpc_private_subnets = var.vpc_private_subnets

  image_family_prefix = var.control_server_image_family_prefix
  cluster_size        = var.control_server_cluster_size
  machine_type        = var.control_server_machine_type

  nomad_acl_token              = var.nomad_acl_token_secret
  consul_acl_token             = var.consul_acl_token_secret
  consul_gossip_encryption_key = var.consul_gossip_encryption_key

}

module "api" {
  source = "../modules/nodepool-api"

  prefix         = var.prefix
  aws_account_id = var.aws_account_id

  cluster_tag_name  = local.cluster_tag_name
  cluster_tag_value = local.cluster_tag_value

  cluster_node_policy_arn      = aws_iam_policy.cluster_node_policy.arn
  cluster_node_ec2_policy_json = data.aws_iam_policy_document.cluster_node_ec2_policy.json

  setup_bucket_name = var.setup_bucket_name
  setup_files_hash  = local.setup_files_hash

  security_group_ids  = var.api_security_group_ids
  target_group_arns   = var.api_target_group_arns
  vpc_private_subnets = var.vpc_private_subnets

  image_family_prefix = var.api_image_family_prefix
  cluster_size        = var.api_cluster_size
  machine_type        = var.api_machine_type

  node_pool_name               = var.api_node_pool_name
  consul_acl_token             = var.consul_acl_token_secret
  consul_gossip_encryption_key = var.consul_gossip_encryption_key
  consul_dns_request_token     = var.consul_dns_request_token_secret

  aws_ecr_account_repository_domain = local.aws_ecr_account_repository_domain
  loki_bucket_arn                   = data.aws_s3_bucket.loki_bucket.arn

}

module "clickhouse" {
  source = "../modules/nodepool-clickhouse"

  prefix         = var.prefix
  aws_account_id = var.aws_account_id

  cluster_tag_name  = local.cluster_tag_name
  cluster_tag_value = local.cluster_tag_value

  cluster_node_policy_arn      = aws_iam_policy.cluster_node_policy.arn
  cluster_node_ec2_policy_json = data.aws_iam_policy_document.cluster_node_ec2_policy.json

  setup_bucket_name = var.setup_bucket_name
  setup_files_hash  = local.setup_files_hash

  security_group_ids = var.clickhouse_security_group_ids

  image_family_prefix = var.clickhouse_image_family_prefix
  cluster_size        = var.clickhouse_cluster_size
  machine_type        = var.clickhouse_machine_type

  node_pool_name                    = var.clickhouse_node_pool_name
  clickhouse_az                     = var.clickhouse_az
  clickhouse_subnet_id              = var.clickhouse_subnet_id
  clickhouse_backups_bucket_arn     = data.aws_s3_bucket.clickhouse_bucket.arn
  job_constraint_prefix             = var.clickhouse_job_constraint_prefix
  consul_acl_token                  = var.consul_acl_token_secret
  consul_gossip_encryption_key      = var.consul_gossip_encryption_key
  consul_dns_request_token          = var.consul_dns_request_token_secret
  aws_ecr_account_repository_domain = local.aws_ecr_account_repository_domain

}

module "build" {
  source = "../modules/nodepool-client"

  name           = "build"
  prefix         = var.prefix
  aws_account_id = var.aws_account_id

  cluster_tag_name  = local.cluster_tag_name
  cluster_tag_value = local.cluster_tag_value

  cluster_node_policy_arn      = aws_iam_policy.cluster_node_policy.arn
  cluster_node_ec2_policy_json = data.aws_iam_policy_document.cluster_node_ec2_policy.json

  setup_bucket_name = var.setup_bucket_name
  setup_files_hash  = local.setup_files_hash

  security_group_ids  = var.build_security_group_ids
  vpc_private_subnets = var.vpc_private_subnets

  image_family_prefix = var.build_image_family_prefix
  cluster_size        = var.build_cluster_size
  machine_type        = var.build_machine_type

  node_pool_name                    = var.build_node_pool_name
  nested_virtualization             = var.build_server_nested_virtualization
  consul_acl_token                  = var.consul_acl_token_secret
  consul_gossip_encryption_key      = var.consul_gossip_encryption_key
  consul_dns_request_token          = var.consul_dns_request_token_secret
  aws_ecr_account_repository_domain = local.aws_ecr_account_repository_domain

  fc_kernels_bucket_name      = var.fc_kernels_bucket_name
  fc_versions_bucket_name     = var.fc_versions_bucket_name
  fc_env_pipeline_bucket_name = var.fc_env_pipeline_bucket_name

  fc_env_pipeline_bucket_arn       = data.aws_s3_bucket.fc_env_pipeline.arn
  fc_kernels_bucket_arn            = data.aws_s3_bucket.fc_kernels.arn
  fc_versions_bucket_arn           = data.aws_s3_bucket.fc_versions.arn
  templates_bucket_arn             = data.aws_s3_bucket.templates_bucket.arn
  templates_build_cache_bucket_arn = data.aws_s3_bucket.templates_build_cache_bucket.arn
  custom_environments_repo_arn     = data.aws_ecr_repository.custom_environments.arn
}

module "client" {
  source = "../modules/nodepool-client"

  prefix         = var.prefix
  aws_account_id = var.aws_account_id

  cluster_tag_name  = local.cluster_tag_name
  cluster_tag_value = local.cluster_tag_value

  cluster_node_policy_arn      = aws_iam_policy.cluster_node_policy.arn
  cluster_node_ec2_policy_json = data.aws_iam_policy_document.cluster_node_ec2_policy.json

  setup_bucket_name = var.setup_bucket_name
  setup_files_hash  = local.setup_files_hash

  security_group_ids  = var.client_security_group_ids
  vpc_private_subnets = var.vpc_private_subnets

  image_family_prefix = var.client_image_family_prefix
  cluster_size        = var.client_cluster_size
  machine_type        = var.client_machine_type

  node_pool_name                    = var.client_node_pool_name
  base_hugepages_percentage         = var.client_base_hugepages_percentage
  nested_virtualization             = var.client_server_nested_virtualization
  consul_acl_token                  = var.consul_acl_token_secret
  consul_gossip_encryption_key      = var.consul_gossip_encryption_key
  consul_dns_request_token          = var.consul_dns_request_token_secret
  aws_ecr_account_repository_domain = local.aws_ecr_account_repository_domain

  fc_kernels_bucket_name      = var.fc_kernels_bucket_name
  fc_versions_bucket_name     = var.fc_versions_bucket_name
  fc_env_pipeline_bucket_name = var.fc_env_pipeline_bucket_name

  fc_env_pipeline_bucket_arn       = data.aws_s3_bucket.fc_env_pipeline.arn
  fc_kernels_bucket_arn            = data.aws_s3_bucket.fc_kernels.arn
  fc_versions_bucket_arn           = data.aws_s3_bucket.fc_versions.arn
  templates_bucket_arn             = data.aws_s3_bucket.templates_bucket.arn
  templates_build_cache_bucket_arn = data.aws_s3_bucket.templates_build_cache_bucket.arn
  custom_environments_repo_arn     = data.aws_ecr_repository.custom_environments.arn

}
