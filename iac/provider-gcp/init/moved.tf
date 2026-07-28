moved {
  from = google_secret_manager_secret_version.consul_acl_token
  to   = google_secret_manager_secret_version.consul_acl_token_legacy
}

moved {
  from = google_secret_manager_secret_version.nomad_acl_token
  to   = google_secret_manager_secret_version.nomad_acl_token_legacy
}
