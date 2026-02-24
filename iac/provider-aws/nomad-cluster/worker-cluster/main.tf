locals {
  startup_script = templatefile("${path.module}/../scripts/start-client.sh", {
    CLUSTER_TAG_NAME             = "orch"
    SCRIPTS_BUCKET               = var.cluster_setup_bucket_name
    FC_KERNELS_BUCKET_NAME       = var.fc_kernels_bucket_name
    FC_VERSIONS_BUCKET_NAME      = var.fc_versions_bucket_name
    FC_ENV_PIPELINE_BUCKET_NAME  = var.fc_env_pipeline_bucket_name
    DOCKER_CONTEXTS_BUCKET_NAME  = var.docker_contexts_bucket_name
    AWS_REGION                   = var.aws_region
    NOMAD_TOKEN                  = var.nomad_acl_token_secret
    CONSUL_TOKEN                 = var.consul_acl_token_secret
    RUN_CONSUL_FILE_HASH         = var.file_hash["scripts/run-consul.sh"]
    RUN_NOMAD_FILE_HASH          = var.file_hash["scripts/run-nomad.sh"]
    CONSUL_GOSSIP_ENCRYPTION_KEY = var.consul_gossip_encryption_key_secret_data
    CONSUL_DNS_REQUEST_TOKEN     = var.consul_dns_request_token_secret_data
    EFS_DNS_NAME                 = var.efs_cache_enabled ? var.efs_dns_name : ""
    EFS_MOUNT_PATH               = var.efs_mount_path
    EFS_MOUNT_SUBDIR             = var.efs_mount_subdir
    USE_EFS_CACHE                = var.efs_cache_enabled
    NODE_POOL                    = var.node_pool
    BASE_HUGEPAGES_PERCENTAGE    = var.hugepages_percentage
  })
}

resource "aws_launch_template" "worker" {
  name_prefix   = "${var.cluster_name}-"
  image_id      = var.ami_id
  instance_type = var.instance_type

  iam_instance_profile {
    name = var.iam_instance_profile_name
  }

  user_data = base64encode(local.startup_script)

  vpc_security_group_ids = var.security_group_ids

  # Enable nested virtualization for non-metal instances (C8i/M8i/R8i)
  dynamic "cpu_options" {
    for_each = var.nested_virtualization ? [1] : []
    content {
      nested_virtualization = "enabled"
    }
  }

  # Use Spot Instances for fault-tolerant workloads (e.g. build clusters)
  dynamic "instance_market_options" {
    for_each = var.use_spot ? [1] : []
    content {
      market_type = "spot"
      spot_options {
        spot_instance_type             = "one-time"
        instance_interruption_behavior = "terminate"
      }
    }
  }

  block_device_mappings {
    device_name = "/dev/sda1"

    ebs {
      volume_size           = var.boot_disk_size_gb
      volume_type           = var.boot_disk_type
      delete_on_termination = true
      encrypted             = true
    }
  }

  # Additional EBS cache disk for non-NVMe instances (replaces instance store)
  dynamic "block_device_mappings" {
    for_each = var.cache_disk_size_gb > 0 ? [1] : []
    content {
      device_name = "/dev/xvdb"

      ebs {
        volume_size           = var.cache_disk_size_gb
        volume_type           = "gp3"
        delete_on_termination = true
        encrypted             = true
      }
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
      Name       = var.cluster_name
      ClusterTag = "orch"
      Role       = "worker"
      NodePool   = var.node_pool
    })
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_autoscaling_group" "worker" {
  name_prefix      = "${var.cluster_name}-"
  desired_capacity = var.cluster_size
  min_size         = try(var.autoscaler.min_size, var.cluster_size)
  max_size         = try(var.autoscaler.max_size, var.cluster_size)

  vpc_zone_identifier = var.subnet_ids

  launch_template {
    id      = aws_launch_template.worker.id
    version = "$Latest"
  }

  health_check_type         = "EC2"
  health_check_grace_period = 600

  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 50
    }
  }

  tag {
    key                 = "Name"
    value               = var.cluster_name
    propagate_at_launch = true
  }

  tag {
    key                 = "ClusterTag"
    value               = "orch"
    propagate_at_launch = true
  }

  tag {
    key                 = "Role"
    value               = "worker"
    propagate_at_launch = true
  }

  tag {
    key                 = "NodePool"
    value               = var.node_pool
    propagate_at_launch = true
  }

  lifecycle {
    create_before_destroy = true
  }
}

# CPU-based autoscaling policy
resource "aws_autoscaling_policy" "cpu" {
  count = try(var.autoscaler.max_size > var.cluster_size, false) ? 1 : 0

  name                   = "${var.cluster_name}-cpu-target"
  autoscaling_group_name = aws_autoscaling_group.worker.name
  policy_type            = "TargetTrackingScaling"

  target_tracking_configuration {
    predefined_metric_specification {
      predefined_metric_type = "ASGAverageCPUUtilization"
    }

    target_value = try(var.autoscaler.cpu_target, 70)
  }
}
