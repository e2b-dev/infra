locals {
  api_pool_name = "${var.prefix}orch-api"
  api_startup_script = templatefile("${path.module}/scripts/start-api.sh", {
    CLUSTER_TAG_NAME             = var.cluster_tag_name
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    FC_KERNELS_BUCKET_NAME       = var.fc_kernels_bucket_name
    FC_VERSIONS_BUCKET_NAME      = var.fc_versions_bucket_name
    FC_ENV_PIPELINE_BUCKET_NAME  = var.fc_env_pipeline_bucket_name
    DOCKER_CONTEXTS_BUCKET_NAME  = var.docker_contexts_bucket_name
    AWS_REGION                   = var.aws_region
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = local.file_hash["scripts/run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = local.file_hash["scripts/run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = random_id.consul_gossip_encryption_key.b64_std
    CONSUL_DNS_REQUEST_TOKEN     = random_uuid.consul_dns_request_token.result
    NODE_POOL                    = "api"
  })
}

resource "aws_launch_template" "api" {
  name_prefix   = "${local.api_pool_name}-"
  image_id      = var.ami_id
  instance_type = var.api_instance_type

  iam_instance_profile {
    name = var.iam_instance_profile_name
  }

  user_data = base64encode(local.api_startup_script)

  vpc_security_group_ids = [var.cluster_sg_id]

  block_device_mappings {
    device_name = "/dev/sda1"

    ebs {
      volume_size           = 200
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
      Name       = local.api_pool_name
      ClusterTag = var.cluster_tag_name
      Role       = "api"
      NodePool   = "api"
    })
  }

  lifecycle {
    create_before_destroy = true
  }

  depends_on = [
    aws_s3_object.setup_config_objects
  ]
}

resource "aws_autoscaling_group" "api" {
  name_prefix      = "${local.api_pool_name}-"
  desired_capacity = var.api_cluster_size
  min_size         = var.api_cluster_size
  max_size         = var.api_cluster_size

  vpc_zone_identifier = var.public_subnet_ids

  launch_template {
    id      = aws_launch_template.api.id
    version = "$Latest"
  }

  health_check_type         = "ELB"
  health_check_grace_period = 600

  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 50
    }
  }

  tag {
    key                 = "Name"
    value               = local.api_pool_name
    propagate_at_launch = true
  }

  tag {
    key                 = "ClusterTag"
    value               = var.cluster_tag_name
    propagate_at_launch = true
  }

  tag {
    key                 = "Role"
    value               = "api"
    propagate_at_launch = true
  }

  tag {
    key                 = "NodePool"
    value               = "api"
    propagate_at_launch = true
  }

  lifecycle {
    create_before_destroy = true
  }
}
