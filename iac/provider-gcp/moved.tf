
moved {
  from = google_secret_manager_secret.postgres_connection_string
  to   = module.init.google_secret_manager_secret.postgres_connection_string
}

moved {
  from = google_secret_manager_secret.posthog_api_key
  to   = module.init.google_secret_manager_secret.posthog_api_key
}

moved {
  from = google_secret_manager_secret_version.posthog_api_key
  to   = module.init.google_secret_manager_secret_version.posthog_api_key
}

moved {
  from = google_secret_manager_secret.redis_cluster_url
  to   = module.init.google_secret_manager_secret.redis_cluster_url
}

moved {
  from = google_secret_manager_secret_version.redis_cluster_url
  to   = module.init.google_secret_manager_secret_version.redis_cluster_url
}

moved {
  from = module.nomad.random_password.clickhouse_password
  to   = random_password.clickhouse_password
}

moved {
  from = module.nomad.random_password.clickhouse_server_secret
  to   = random_password.clickhouse_server_secret
}
