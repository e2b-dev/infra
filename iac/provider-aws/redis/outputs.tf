output "redis_endpoint" {
  value = aws_elasticache_replication_group.redis.configuration_endpoint_address
}

output "redis_port" {
  value = aws_elasticache_replication_group.redis.port
}

output "replication_group_id" {
  description = "ElastiCache replication group ID for CloudWatch monitoring"
  value       = aws_elasticache_replication_group.redis.replication_group_id
}
