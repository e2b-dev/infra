
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
  from = google_secret_manager_secret.supabase_jwt_secrets
  to   = module.init.google_secret_manager_secret.supabase_jwt_secrets
}

moved {
  from = google_secret_manager_secret_version.supabase_jwt_secrets
  to   = module.init.google_secret_manager_secret_version.supabase_jwt_secrets
}

moved {
  from = random_password.api_admin_secret
  to   = module.init.random_password.api_admin_secret
}

moved {
  from = google_secret_manager_secret.api_admin_token
  to   = module.init.google_secret_manager_secret.api_admin_token
}

moved {
  from = google_secret_manager_secret_version.api_admin_token_value
  to   = module.init.google_secret_manager_secret_version.api_admin_token_value
}

moved {
  from = random_password.dashboard_api_admin_secret
  to   = module.init.random_password.dashboard_api_admin_secret
}

moved {
  from = google_secret_manager_secret.dashboard_api_admin_token
  to   = module.init.google_secret_manager_secret.dashboard_api_admin_token
}

moved {
  from = google_secret_manager_secret_version.dashboard_api_admin_token_value
  to   = module.init.google_secret_manager_secret_version.dashboard_api_admin_token_value
}
