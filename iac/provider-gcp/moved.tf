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
