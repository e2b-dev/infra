locals {
  server_pool_name = "${var.prefix}orch-server"
  server_startup_script = templatefile("${path.module}/scripts/start-server.sh", {
    NUM_SERVERS                  = var.server_cluster_size
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.file_hash["scripts/run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.file_hash["scripts/run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = random_id.consul_gossip_encryption_key.b64_std
    AWS_REGION                   = var.aws_region
  })
}

resource "aws_launch_template" "server" {
  name_prefix   = "${local.server_pool_name}-"
  image_id      = var.ami_id
  instance_type = var.server_instance_type

  iam_instance_profile {
    name = var.iam_instance_profile_name
  }

  user_data = base64encode(local.server_startup_script)

  vpc_security_group_ids = [var.cluster_sg_id]

  block_device_mappings {
    device_name = "/dev/sda1"

    ebs {
      volume_size           = 20
      volume_type           = "gp3"
      delete_on_termination = true
      encrypted             = true
    }
  }

  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }

  tag_specifications {
    resource_type = "instance"

    tags = merge(var.tags, {
      Name       = local.server_pool_name
      ClusterTag = var.cluster_tag_name
      Role       = "server"
    })
  }

  lifecycle {
    create_before_destroy = true
  }

  depends_on = [
    aws_s3_object.setup_config_objects
  ]
}

resource "aws_autoscaling_group" "server" {
  name_prefix      = "${local.server_pool_name}-"
  desired_capacity = var.server_cluster_size
  min_size         = var.server_cluster_size
  max_size         = var.server_cluster_size

  vpc_zone_identifier = var.public_subnet_ids

  launch_template {
    id      = aws_launch_template.server.id
    version = "$Latest"
  }

  health_check_type         = "EC2"
  health_check_grace_period = 120

  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 90
    }
  }

  tag {
    key                 = "Name"
    value               = local.server_pool_name
    propagate_at_launch = true
  }

  tag {
    key                 = "ClusterTag"
    value               = var.cluster_tag_name
    propagate_at_launch = true
  }

  tag {
    key                 = "Role"
    value               = "server"
    propagate_at_launch = true
  }

  lifecycle {
    create_before_destroy = false
  }
}
