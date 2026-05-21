locals {
  dashboard_api_deployed = var.dashboard_api_count > 0

  billing_server_url_secret_id       = "${var.prefix}billing-server-url"
  billing_server_api_token_secret_id = "${var.prefix}billing-server-api-token"
}

data "google_secret_manager_secrets" "billing_server_url" {
  count   = local.dashboard_api_deployed ? 1 : 0
  project = var.gcp_project_id
  filter  = "name:${local.billing_server_url_secret_id}"
}

data "google_secret_manager_secrets" "billing_server_api_token" {
  count   = local.dashboard_api_deployed ? 1 : 0
  project = var.gcp_project_id
  filter  = "name:${local.billing_server_api_token_secret_id}"
}

locals {
  billing_server_url_secret_exists       = try(length(data.google_secret_manager_secrets.billing_server_url[0].secrets) > 0, false)
  billing_server_api_token_secret_exists = try(length(data.google_secret_manager_secrets.billing_server_api_token[0].secrets) > 0, false)
}

data "google_secret_manager_secret_version" "billing_server_url" {
  count   = local.billing_server_url_secret_exists ? 1 : 0
  project = var.gcp_project_id
  secret  = local.billing_server_url_secret_id
}

data "google_secret_manager_secret_version" "billing_server_api_token" {
  count   = local.billing_server_api_token_secret_exists ? 1 : 0
  project = var.gcp_project_id
  secret  = local.billing_server_api_token_secret_id
}

locals {
  dashboard_api_billing_server_url       = try(trimspace(data.google_secret_manager_secret_version.billing_server_url[0].secret_data), "")
  dashboard_api_billing_server_api_token = try(trimspace(data.google_secret_manager_secret_version.billing_server_api_token[0].secret_data), "")
}
