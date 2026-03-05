// ---
// Buckets
// ---
output "setup_bucket_name" {
  value = aws_s3_bucket.setup.bucket
}

output "fc_template_build_cache_bucket_name" {
  value = aws_s3_bucket.fc_template_build_cache.bucket
}

output "fc_template_bucket_name" {
  value = aws_s3_bucket.fc_templates.bucket
}

output "fc_env_pipeline_bucket_name" {
  value = aws_s3_bucket.fc_env_pipeline.bucket
}

output "fc_kernels_bucket_name" {
  value = aws_s3_bucket.fc_kernels.bucket
}

output "fc_versions_bucket_name" {
  value = aws_s3_bucket.fc_versions.bucket
}

output "load_balancer_logs_bucket_name" {
  value = aws_s3_bucket.load_balancer_logs.bucket
}

output "loki_bucket_name" {
  value = aws_s3_bucket.loki_storage.bucket
}

output "clickhouse_backups_bucket_name" {
  value = aws_s3_bucket.clickhouse_backups.bucket
}

// ---
// ECR Repositories
// ---
output "client_proxy_repository_name" {
  value = aws_ecr_repository.client_proxy.name
}

output "clickhouse_migrator_repository_name" {
  value = aws_ecr_repository.clickhouse_migrator.name
}

output "custom_environments_repository_name" {
  value = aws_ecr_repository.custom_environments.name
}

output "api_repository_name" {
  value = aws_ecr_repository.api.name
}

output "db_migrator_repository_name" {
  value = aws_ecr_repository.db_migrator.name
}

// ---
// Cloudflare
// ---
output "cloudflare" {
  value = module.cloudflare.cloudflare
}

// ---
// Network
// ---
output "vpc_id" {
  value = module.network.vpc_id
}

output "vpc_public_subnet_ids" {
  value = module.network.vpc_public_subnet_ids
}

output "vpc_private_subnet_ids" {
  value = module.network.vpc_private_subnets
}

output "vpc_elasticache_subnet_group_name" {
  value = module.network.elasticache_subnet_group_name
}

output "vpc_instance_connect_security_group_id" {
  value = module.network.instance_connect_security_group_id
}
