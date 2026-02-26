resource "aws_elasticache_subnet_group" "redis" {
  name       = "${var.prefix}redis"
  subnet_ids = var.subnet_ids

  tags = var.tags
}

resource "aws_elasticache_parameter_group" "redis" {
  name   = "${var.prefix}redis7"
  family = "redis7"

  parameter {
    name  = "appendonly"
    value = "yes"
  }

  parameter {
    name  = "appendfsync"
    value = "everysec"
  }

  tags = var.tags
}

resource "random_password" "redis_auth_token" {
  length           = 64
  special          = true
  override_special = "!&#$^<>-"
}

resource "aws_elasticache_replication_group" "redis" {
  replication_group_id = "${var.prefix}redis"
  description          = "E2B Redis cluster"

  node_type               = var.redis_node_type
  num_node_groups         = var.redis_shard_count
  replicas_per_node_group = var.redis_replica_count

  subnet_group_name    = aws_elasticache_subnet_group.redis.name
  security_group_ids   = [var.redis_sg_id]
  parameter_group_name = aws_elasticache_parameter_group.redis.name

  automatic_failover_enabled = true
  multi_az_enabled           = true
  transit_encryption_enabled = true
  at_rest_encryption_enabled = true
  auth_token                 = random_password.redis_auth_token.result

  port = 6379

  maintenance_window = "sun:01:00-sun:03:00"

  tags = merge(var.tags, {
    Name = "${var.prefix}redis"
  })
}

# Write the connection URL to Secrets Manager
resource "aws_secretsmanager_secret_version" "redis_cluster_url" {
  secret_id     = var.redis_cluster_url_secret_arn
  secret_string = "rediss://:${random_password.redis_auth_token.result}@${var.redis_shard_count > 1 ? aws_elasticache_replication_group.redis.configuration_endpoint_address : aws_elasticache_replication_group.redis.primary_endpoint_address}:6379"
}
