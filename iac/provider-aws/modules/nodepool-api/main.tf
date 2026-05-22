locals {
  scripts_path = var.scripts_path != "" ? var.scripts_path : "${path.module}/scripts"

  user_data = templatefile("${local.scripts_path}/start-api.sh", {
    NODE_POOL                    = var.node_pool_name
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    CLUSTER_TAG_VALUE            = var.cluster_tag_value
    SCRIPTS_BUCKET               = var.setup_bucket_name
    CONSUL_TOKEN                 = var.consul_acl_token
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key
    CONSUL_DNS_REQUEST_TOKEN     = var.consul_dns_request_token

    AWS_ECR_ACCOUNT_REPOSITORY_DOMAIN = var.aws_ecr_account_repository_domain

    RUN_CONSUL_FILE_HASH = var.setup_files_hash["run-consul"]
    RUN_NOMAD_FILE_HASH  = var.setup_files_hash["run-nomad"]
  })
}

resource "aws_iam_policy" "api_node_policy" {
  name   = "${var.prefix}api-node-policy"
  policy = data.aws_iam_policy_document.api_node_policy.json
}

data "aws_iam_policy_document" "api_node_policy" {
  statement {
    effect = "Allow"
    actions = [
      "s3:*",
    ]
    resources = [
      "${var.loki_bucket_arn}/*",
      var.loki_bucket_arn,
    ]
  }
}

resource "aws_iam_role" "api" {
  name               = "${var.prefix}api-node"
  assume_role_policy = var.cluster_node_ec2_policy_json
}

resource "aws_iam_role_policy_attachment" "api" {
  for_each = {
    "cluster-node" = var.cluster_node_policy_arn
    "api-node"     = aws_iam_policy.api_node_policy.arn
  }

  role       = aws_iam_role.api.name
  policy_arn = each.value
}

resource "aws_iam_instance_profile" "api" {
  name = "${var.prefix}api-node"
  role = aws_iam_role.api.name
}

data "aws_ami" "api" {
  most_recent = true
  owners      = [var.aws_account_id]

  filter {
    name   = "name"
    values = ["${var.image_family_prefix}*"]
  }
}

resource "aws_launch_template" "api" {
  name          = "${var.prefix}api-node"
  image_id      = data.aws_ami.api.id
  instance_type = var.machine_type
  user_data     = base64encode(local.user_data)

  vpc_security_group_ids = var.security_group_ids

  metadata_options {
    http_tokens = "required"
  }

  iam_instance_profile {
    name = aws_iam_instance_profile.api.name
  }

  block_device_mappings {
    device_name = "/dev/sda1"

    ebs {
      volume_size           = 20
      volume_type           = "gp3"
      delete_on_termination = true
    }
  }

  tag_specifications {
    resource_type = "instance"

    tags = {
      Name = "${var.prefix}orch-api"

      // Tag to identify Nomad cluster members so auto-join can work
      (var.cluster_tag_name) = var.cluster_tag_value
    }
  }
}

resource "aws_autoscaling_group" "api" {
  name                = "${var.prefix}api"
  vpc_zone_identifier = var.vpc_private_subnets
  health_check_type   = "EC2"

  min_size = var.cluster_size
  max_size = var.cluster_size

  // Automatically register instances in the target group for load balancing
  target_group_arns = var.target_group_arns

  launch_template {
    id      = aws_launch_template.api.id
    version = aws_launch_template.api.latest_version
  }

  lifecycle {
    ignore_changes = [desired_capacity]
  }
}
