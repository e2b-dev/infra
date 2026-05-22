locals {
  scripts_path = var.scripts_path != "" ? var.scripts_path : "${path.module}/scripts"

  user_data = templatefile("${local.scripts_path}/start-client.sh", {
    NODE_POOL                    = var.node_pool_name
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    CLUSTER_TAG_VALUE            = var.cluster_tag_value
    SCRIPTS_BUCKET               = var.setup_bucket_name
    CONSUL_TOKEN                 = var.consul_acl_token
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key
    CONSUL_DNS_REQUEST_TOKEN     = var.consul_dns_request_token

    FC_KERNELS_BUCKET_NAME      = var.fc_kernels_bucket_name
    FC_VERSIONS_BUCKET_NAME     = var.fc_versions_bucket_name
    FC_ENV_PIPELINE_BUCKET_NAME = var.fc_env_pipeline_bucket_name
    FC_BUSYBOX_BUCKET_NAME      = var.fc_busybox_bucket_name
    NODE_LABELS                 = join(",", var.node_labels)
    BASE_HUGEPAGES_PERCENTAGE   = var.base_hugepages_percentage

    AWS_ECR_ACCOUNT_REPOSITORY_DOMAIN = var.aws_ecr_account_repository_domain

    RUN_CONSUL_FILE_HASH = var.setup_files_hash["run-consul"]
    RUN_NOMAD_FILE_HASH  = var.setup_files_hash["run-nomad"]
  })
}

resource "aws_iam_policy" "client_node_policy" {
  name   = "${var.prefix}${var.name}-node-policy"
  policy = data.aws_iam_policy_document.client_node_policy.json
}

data "aws_iam_policy_document" "client_node_policy" {
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
      var.custom_environments_repo_arn
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
      "${var.fc_env_pipeline_bucket_arn}/*",
      var.fc_env_pipeline_bucket_arn,

      "${var.fc_kernels_bucket_arn}/*",
      var.fc_kernels_bucket_arn,

      "${var.fc_versions_bucket_arn}/*",
      var.fc_versions_bucket_arn,

      "${var.fc_busybox_bucket_arn}/*",
      var.fc_busybox_bucket_arn,
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
      "${var.templates_bucket_arn}/*",
      var.templates_bucket_arn,

      // Template build cache
      "${var.templates_build_cache_bucket_arn}/*",
      var.templates_build_cache_bucket_arn,
    ]
  }
}

resource "aws_iam_role" "client" {
  name               = "${var.prefix}${var.name}-node"
  assume_role_policy = var.cluster_node_ec2_policy_json
}

resource "aws_iam_role_policy_attachment" "client" {
  for_each = {
    "cluster-node" = var.cluster_node_policy_arn
    "client-node"  = aws_iam_policy.client_node_policy.arn
  }

  role       = aws_iam_role.client.name
  policy_arn = each.value
}

resource "aws_iam_instance_profile" "client" {
  name = "${var.prefix}${var.name}-node"
  role = aws_iam_role.client.name
}

data "aws_ami" "client" {
  most_recent = true
  owners      = [var.aws_account_id]

  filter {
    name   = "name"
    values = ["${var.image_family_prefix}*"]
  }
}

resource "aws_launch_template" "client" {
  name          = "${var.prefix}${var.name}-node"
  image_id      = data.aws_ami.client.id
  instance_type = var.machine_type
  user_data     = base64encode(local.user_data)

  vpc_security_group_ids = var.security_group_ids

  metadata_options {
    http_tokens = "required"
  }

  iam_instance_profile {
    name = aws_iam_instance_profile.client.name
  }

  block_device_mappings {
    device_name = "/dev/sda1"

    ebs {
      volume_size           = var.boot_disk_size_gb
      volume_type           = "gp3"
      delete_on_termination = true
    }
  }

  cpu_options {
    nested_virtualization = var.nested_virtualization ? "enabled" : "disabled"
  }

  tag_specifications {
    resource_type = "instance"

    tags = {
      Name = "${var.prefix}${var.name}"

      // Tag to identify Nomad cluster members so auto-join can work
      (var.cluster_tag_name) = var.cluster_tag_value
    }
  }
}

resource "aws_autoscaling_group" "client" {
  name                = "${var.prefix}${var.name}"
  vpc_zone_identifier = var.vpc_private_subnets
  health_check_type   = "EC2"

  min_size = var.cluster_size
  max_size = var.cluster_size

  launch_template {
    id      = aws_launch_template.client.id
    version = aws_launch_template.client.latest_version
  }

  // Do not wait for slow EC2 Metal instance to terminate before detaching from ASG
  force_delete           = true
  force_delete_warm_pool = true

  lifecycle {
    ignore_changes = [desired_capacity]
  }
}
