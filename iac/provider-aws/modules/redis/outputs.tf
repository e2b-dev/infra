output "endpoint_address" {
  value = aws_elasticache_replication_group.instance.configuration_endpoint_address
}

output "endpoint_ca_pem_base64" {
  value = local.redis_ca_pem_base64
}