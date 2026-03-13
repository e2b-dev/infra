locals {
  scripts_path = var.scripts_path != "" ? var.scripts_path : "${path.module}/scripts"

  user_data = templatefile("${local.scripts_path}/start-server.sh", {
    NUM_SERVERS                  = var.cluster_size
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    CLUSTER_TAG_VALUE            = var.cluster_tag_value
    SCRIPTS_BUCKET               = var.setup_bucket_name
    NOMAD_TOKEN                  = var.nomad_acl_token
    CONSUL_TOKEN                 = var.consul_acl_token
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key

    RUN_CONSUL_FILE_HASH = var.setup_files_hash["run-consul"]
    RUN_NOMAD_FILE_HASH  = var.setup_files_hash["run-nomad"]
  })
}

resource "aws_iam_role" "control_server" {
  name               = "${var.prefix}control-server-node"
  assume_role_policy = var.cluster_node_ec2_policy_json
}

resource "aws_iam_role_policy_attachment" "control_server" {
  for_each = {
    "cluster-node" = var.cluster_node_policy_arn
  }

  role       = aws_iam_role.control_server.name
  policy_arn = each.value
}

data "aws_ami" "control_server" {
  most_recent = true
  owners      = [var.aws_account_id]

  filter {
    name   = "name"
    values = ["${var.image_family_prefix}*"]
  }
}

resource "aws_iam_instance_profile" "control_server" {
  name = "${var.prefix}control-server-node"
  role = aws_iam_role.control_server.name
}

resource "aws_launch_template" "control_server" {
  name          = "${var.prefix}control-server-node"
  image_id      = data.aws_ami.control_server.id
  instance_type = var.machine_type
  user_data     = base64encode(local.user_data)

  vpc_security_group_ids = var.security_group_ids

  iam_instance_profile {
    name = aws_iam_instance_profile.control_server.name
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
      Name = "${var.prefix}orch-server"

      // Tag to identify Nomad cluster members so auto-join can work
      (var.cluster_tag_name) = var.cluster_tag_value

      // Used by Consul to setup properly bootstrap servers number
      cluster-size = tostring(var.cluster_size)
    }
  }
}

resource "aws_autoscaling_group" "control_server" {
  name                = "${var.prefix}control-server"
  vpc_zone_identifier = var.vpc_private_subnets

  min_size = var.cluster_size
  max_size = var.cluster_size

  launch_template {
    id      = aws_launch_template.control_server.id
    version = aws_launch_template.control_server.latest_version
  }

  // Automatically register instances in the target group for load balancing
  target_group_arns = var.target_group_arns

  health_check_type         = "ELB"
  health_check_grace_period = 600

  lifecycle {
    ignore_changes = [desired_capacity]
  }
}
