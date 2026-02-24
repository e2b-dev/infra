output "iam_instance_profile_name" {
  value = aws_iam_instance_profile.infra_instances.name
}

output "iam_role_arn" {
  value = aws_iam_role.infra_instances.arn
}

output "consul_acl_token_secret" {
  value = aws_secretsmanager_secret_version.consul_acl_token.secret_string
}

output "nomad_acl_token_secret" {
  value = aws_secretsmanager_secret_version.nomad_acl_token.secret_string
}

output "core_repository_url" {
  value = aws_ecr_repository.core.repository_url
}

output "core_repository_name" {
  value = aws_ecr_repository.core.name
}

output "orchestration_repository_url" {
  value = aws_ecr_repository.orchestration.repository_url
}

# Secret ARNs
output "cloudflare_api_token_secret_arn" {
  value = aws_secretsmanager_secret.cloudflare_api_token.arn
}

output "routing_domains_secret_name" {
  value = aws_secretsmanager_secret.routing_domains.name
}

output "postgres_connection_string_secret_arn" {
  value = aws_secretsmanager_secret.postgres_connection_string.arn
}

output "postgres_read_replica_connection_string_secret_arn" {
  value = aws_secretsmanager_secret.postgres_read_replica_connection_string.arn
}

output "supabase_jwt_secrets_secret_arn" {
  value = aws_secretsmanager_secret.supabase_jwt_secrets.arn
}

output "posthog_api_key_secret_arn" {
  value = aws_secretsmanager_secret.posthog_api_key.arn
}

output "analytics_collector_host_secret_arn" {
  value = aws_secretsmanager_secret.analytics_collector_host.arn
}

output "analytics_collector_api_token_secret_arn" {
  value = aws_secretsmanager_secret.analytics_collector_api_token.arn
}

output "launch_darkly_api_key_secret_arn" {
  value = aws_secretsmanager_secret.launch_darkly_api_key.arn
}

output "redis_cluster_url_secret_arn" {
  value = aws_secretsmanager_secret.redis_cluster_url.arn
}

output "redis_tls_ca_base64_secret_arn" {
  value = aws_secretsmanager_secret.redis_tls_ca_base64.arn
}

# Bucket names
output "loki_bucket_name" {
  value = aws_s3_bucket.loki_storage.id
}

output "envs_docker_context_bucket_name" {
  value = aws_s3_bucket.envs_docker_context.id
}

output "cluster_setup_bucket_name" {
  value = aws_s3_bucket.instance_setup.id
}

output "fc_env_pipeline_bucket_name" {
  value = aws_s3_bucket.fc_env_pipeline.id
}

output "fc_kernels_bucket_name" {
  value = aws_s3_bucket.fc_kernels.id
}

output "fc_versions_bucket_name" {
  value = aws_s3_bucket.fc_versions.id
}

output "fc_template_bucket_name" {
  value = aws_s3_bucket.fc_templates.id
}

output "fc_build_cache_bucket_name" {
  value = aws_s3_bucket.fc_build_cache.id
}

output "clickhouse_backups_bucket_name" {
  value = aws_s3_bucket.clickhouse_backups.id
}

output "dockerhub_username_secret_arn" {
  value = aws_secretsmanager_secret.dockerhub_username.arn
}

output "dockerhub_password_secret_arn" {
  value = aws_secretsmanager_secret.dockerhub_password.arn
}
