output "redis_endpoint" {
  value = aws_elasticache_replication_group.redis.configuration_endpoint_address
}

output "redis_port" {
  value = aws_elasticache_replication_group.redis.port
}
