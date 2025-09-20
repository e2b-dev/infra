moved {
  from = module.buckets.google_storage_bucket_iam_member.envs_pipeline_iam
  to   = module.init.google_storage_bucket_iam_member.envs_pipeline_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.envs_docker_context_iam
  to   = module.init.google_storage_bucket_iam_member.envs_docker_context_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.fc_build_cache_bucket_iam
  to   = module.init.google_storage_bucket_iam_member.fc_build_cache_bucket_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.fc_kernels_bucket_iam
  to   = module.init.google_storage_bucket_iam_member.fc_kernels_bucket_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.fc_template_bucket_iam
  to   = module.init.google_storage_bucket_iam_member.fc_template_bucket_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.instance_setup_bucket_iam
  to   = module.init.google_storage_bucket_iam_member.instance_setup_bucket_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.loki_storage_iam
  to   = module.init.google_storage_bucket_iam_member.loki_storage_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.fc_versions_bucket_iam
  to   = module.init.google_storage_bucket_iam_member.fc_versions_bucket_iam
}

moved {
  from = module.buckets.google_storage_bucket_iam_member.fc_template_bucket_iam_reader
  to   = module.init.google_storage_bucket_iam_member.fc_template_bucket_iam_reader
}

moved {
  from = module.buckets.google_storage_bucket.clickhouse_backups_bucket
  to   = module.init.google_storage_bucket.clickhouse_backups_bucket
}

moved {
  from = module.buckets.google_storage_bucket.envs_docker_context
  to   = module.init.google_storage_bucket.envs_docker_context
}

moved {
  from = module.buckets.google_storage_bucket.fc_build_cache_bucket
  to   = module.init.google_storage_bucket.fc_build_cache_bucket
}

moved {
  from = module.buckets.google_storage_bucket.vault_backend
  to   = module.init.google_storage_bucket.vault_backend
}

moved {
  from = module.buckets.google_storage_bucket.fc_env_pipeline_bucket
  to   = module.init.google_storage_bucket.fc_env_pipeline_bucket
}

moved {
  from = module.buckets.google_storage_bucket.fc_kernels_bucket
  to   = module.init.google_storage_bucket.fc_kernels_bucket
}

moved {
  from = module.buckets.google_storage_bucket.fc_template_bucket
  to   = module.init.google_storage_bucket.fc_template_bucket
}

moved {
  from = module.buckets.google_storage_bucket.fc_versions_bucket
  to   = module.init.google_storage_bucket.fc_versions_bucket
}

moved {
  from = module.buckets.google_storage_bucket.loki_storage_bucket
  to   = module.init.google_storage_bucket.loki_storage_bucket
}

moved {
  from = module.buckets.google_storage_bucket.setup_bucket
  to   = module.init.google_storage_bucket.setup_bucket
}

moved {
  from = module.cluster.module.server_cluster.google_compute_instance_group_manager.server_cluster
  to   = module.cluster.google_compute_instance_group_manager.server_pool
}

moved {
  from = module.cluster.module.server_cluster.google_compute_health_check.nomad_check
  to   = module.cluster.google_compute_health_check.server_nomad_check
}

moved {
  from = module.cluster.module.client_cluster.google_compute_region_instance_group_manager.client_cluster
  to   = module.cluster.google_compute_region_instance_group_manager.client_pool
}

moved {
  from = module.cluster.module.client_cluster.google_compute_instance_template.client
  to   = module.cluster.google_compute_instance_template.client
}

moved {
  from = module.cluster.module.client_cluster.google_compute_health_check.nomad_check
  to   = module.cluster.google_compute_health_check.client_nomad_check
}

moved {
  from = module.cluster.module.server_cluster.google_compute_instance_template.server
  to   = module.cluster.google_compute_instance_template.server
}


moved {
  from = module.cluster.module.clickhouse_cluster.google_compute_per_instance_config.instances
  to   = module.cluster.google_compute_per_instance_config.clickhouse_instances
}

moved {
  from = module.cluster.module.clickhouse_cluster.google_compute_disk.stateful_disk
  to   = module.cluster.google_compute_disk.clickhouse_stateful_disk
}

moved {
  from = module.cluster.module.clickhouse_cluster.google_compute_instance_template.server
  to   = module.cluster.google_compute_instance_template.clickhouse
}

moved {
  from = module.cluster.module.clickhouse_cluster.google_compute_instance_group_manager.cluster
  to   = module.cluster.google_compute_instance_group_manager.clickhouse_pool
}

moved {
  from = module.cluster.module.clickhouse_cluster.google_compute_health_check.nomad_check
  to   = module.cluster.google_compute_health_check.clickhouse_nomad_check
}

moved {
  from = module.cluster.module.build_cluster.google_compute_instance_template.build
  to   = module.cluster.google_compute_instance_template.build
}

moved {
  from = module.cluster.module.build_cluster.google_compute_instance_group_manager.build_cluster
  to   = module.cluster.google_compute_instance_group_manager.build_pool
}

moved {
  from = module.cluster.module.build_cluster.google_compute_health_check.nomad_check
  to   = module.cluster.google_compute_health_check.build_nomad_check
}

moved {
  from = module.cluster.module.api_cluster.google_compute_instance_template.api
  to   = module.cluster.google_compute_instance_template.api
}

moved {
  from = module.cluster.module.api_cluster.google_compute_instance_group_manager.api_cluster
  to   = module.cluster.google_compute_instance_group_manager.api_pool
}

moved {
  from = module.cluster.module.api_cluster.google_compute_health_check.nomad_check
  to   = module.cluster.google_compute_health_check.api_nomad_check
}

moved {
  from = module.api.google_artifact_registry_repository.custom_environments_repository
  to   = google_artifact_registry_repository.custom_environments_repository
}

moved {
  from = module.api.google_artifact_registry_repository_iam_member.custom_environments_repository_member
  to   = google_artifact_registry_repository_iam_member.custom_environments_repository_member
}

moved {
  from = module.api.google_secret_manager_secret.api_admin_token
  to   = google_secret_manager_secret.api_admin_token
}

moved {
  from = module.api.google_secret_manager_secret.api_secret
  to   = google_secret_manager_secret.api_secret
}

moved {
  from = module.api.google_secret_manager_secret.postgres_connection_string
  to   = google_secret_manager_secret.postgres_connection_string
}

moved {
  from = module.api.google_secret_manager_secret.posthog_api_key
  to   = google_secret_manager_secret.posthog_api_key
}

moved {
  from = module.api.google_secret_manager_secret.redis_url
  to   = google_secret_manager_secret.redis_url
}

moved {
  from = module.api.google_secret_manager_secret.sandbox_access_token_hash_seed
  to   = google_secret_manager_secret.sandbox_access_token_hash_seed
}

moved {
  from = module.api.google_secret_manager_secret.supabase_jwt_secrets
  to   = google_secret_manager_secret.supabase_jwt_secrets
}

moved {
  from = module.api.google_secret_manager_secret_version.api_admin_token_value
  to   = google_secret_manager_secret_version.api_admin_token_value
}

moved {
  from = module.api.google_secret_manager_secret_version.api_secret_value
  to   = google_secret_manager_secret_version.api_secret_value
}

moved {
  from = module.api.google_secret_manager_secret_version.posthog_api_key
  to   = google_secret_manager_secret_version.posthog_api_key
}

moved {
  from = module.api.google_secret_manager_secret_version.redis_url
  to   = google_secret_manager_secret_version.redis_url
}

moved {
  from = module.api.google_secret_manager_secret_version.sandbox_access_token_hash_seed
  to   = google_secret_manager_secret_version.sandbox_access_token_hash_seed
}

moved {
  from = module.api.google_secret_manager_secret_version.supabase_jwt_secrets
  to   = google_secret_manager_secret_version.supabase_jwt_secrets
}

moved {
  from = module.api.random_password.api_admin_secret
  to   = random_password.api_admin_secret
}

moved {
  from = module.api.random_password.api_secret
  to   = random_password.api_secret
}

moved {
  from = module.api.random_password.sandbox_access_token_hash_seed
  to   = random_password.sandbox_access_token_hash_seed
}

moved {
  from = module.client_proxy.google_secret_manager_secret.edge_api_secret
  to   = google_secret_manager_secret.edge_api_secret
}

moved {
  from = module.client_proxy.google_secret_manager_secret_version.edge_api_secret
  to   = google_secret_manager_secret_version.edge_api_secret
}

moved {
  from = module.client_proxy.random_password.edge_api_secret
  to   = random_password.edge_api_secret
}

moved {
  from = module.docker_reverse_proxy.google_artifact_registry_repository_iam_member.orchestration_repository_member
  to   = google_artifact_registry_repository_iam_member.orchestration_repository_member
}

moved {
  from = module.docker_reverse_proxy.google_service_account.docker_registry_service_account
  to   = google_service_account.docker_registry_service_account
}

moved {
  from = module.docker_reverse_proxy.google_service_account_key.google_service_key
  to   = google_service_account_key.google_service_key
}
