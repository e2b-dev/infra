locals {
  scripts_path = var.scripts_path != "" ? var.scripts_path : "${path.module}/scripts"
}

resource "aws_iam_policy" "clickhouse_node_policy" {
  name   = "${var.prefix}clickhouse-node-policy"
  policy = data.aws_iam_policy_document.clickhouse_node_policy.json
}

data "aws_iam_policy_document" "clickhouse_node_policy" {
  statement {
    effect = "Allow"
    actions = [
      "s3:GetBucketVersioning",
      "s3:ListBucket",

      "s3:DeleteObject",
      "s3:GetObject",
      "s3:PutObject"
    ]
    resources = [
      "${var.clickhouse_backups_bucket_arn}/*",
      var.clickhouse_backups_bucket_arn,
    ]
  }
}

resource "aws_iam_role" "clickhouse" {
  name               = "${var.prefix}clickhouse-node"
  assume_role_policy = var.cluster_node_ec2_policy_json
}

resource "aws_iam_role_policy_attachment" "clickhouse" {
  for_each = {
    "cluster-node"    = var.cluster_node_policy_arn
    "clickhouse-node" = aws_iam_policy.clickhouse_node_policy.arn
  }

  role       = aws_iam_role.clickhouse.name
  policy_arn = each.value
}

resource "aws_iam_instance_profile" "clickhouse" {
  name = "${var.prefix}clickhouse-node"
  role = aws_iam_role.clickhouse.name
}

data "aws_ami" "clickhouse" {
  most_recent = true
  owners      = [var.aws_account_id]

  filter {
    name   = "name"
    values = ["${var.image_family_prefix}*"]
  }
}

resource "aws_ebs_volume" "clickhouse" {
  for_each = toset([for i in range(1, var.cluster_size + 1) : tostring(i)])

  availability_zone = var.clickhouse_az
  size              = var.data_volume_size_gb
  type              = "gp3"

  tags = {
    Name = "${var.prefix}clickhouse-data-${each.key}"
  }
}

resource "aws_launch_template" "clickhouse" {
  name          = "${var.prefix}clickhouse-node"
  image_id      = data.aws_ami.clickhouse.id
  instance_type = var.machine_type

  vpc_security_group_ids = var.security_group_ids

  iam_instance_profile {
    name = aws_iam_instance_profile.clickhouse.name
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
      // Tag to identify Nomad cluster members so auto-join can work
      (var.cluster_tag_name) = var.cluster_tag_value
    }
  }
}

// Launch N Clickhouse instances from the launch template and attach EBS volumes
// We cannot use an autoscaling group here because we need to attach specific EBS volumes to each instance and keep them persistent
resource "aws_instance" "clickhouse" {
  for_each = toset([for i in range(1, var.cluster_size + 1) : tostring(i)])

  launch_template {
    id      = aws_launch_template.clickhouse.id
    version = "$Latest"
  }

  availability_zone = var.clickhouse_az
  subnet_id         = var.clickhouse_subnet_id

  user_data = base64encode(templatefile("${local.scripts_path}/start-clickhouse.sh", {
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

    // We are using EBS volume ID to find device name (that is random in new EC2 instances)
    // and then mount this drive to clickhouse data directory.
    // Volume ID is used in nvme device metadata but without "-" so we need to strip it here as well.
    EBS_VOLUME_ID = replace(aws_ebs_volume.clickhouse[each.key].id, "-", "")
  }))

  tags = {
    Name = "${var.prefix}orch-clickhouse-${each.key}"

    // This tag is used by Nomad clients to set job constraints
    // so we are not scheduling Clickhouse jobs on other nodes with wrong constraints index key
    // as we need to match instance, volume and nomad job index keys correctly
    "job-constraint" = "${var.job_constraint_prefix}-${each.key}"
  }
}

resource "aws_volume_attachment" "clickhouse" {
  for_each = toset([for i in range(1, var.cluster_size + 1) : tostring(i)])

  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.clickhouse[each.key].id
  instance_id = aws_instance.clickhouse[each.key].id
}
