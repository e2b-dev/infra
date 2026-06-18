data "google_secret_manager_secret_version" "dashboard_api_admin_token" {
  secret = module.init.dashboard_api_admin_token_secret_name
}

data "google_secret_manager_secret_version" "ory_project_api_key" {
  count   = local.dashboard_api_deployed && module.init.ory_project_api_key_secret_exists ? 1 : 0
  project = var.gcp_project_id
  secret  = module.init.ory_project_api_key_secret_name
  version = "latest"
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
  dashboard_api_deployed = var.dashboard_api_count > 0

  billing_server_url_secret_id       = "${var.prefix}billing-server-url"
  billing_server_api_token_secret_id = "${var.prefix}billing-server-api-token"

  billing_server_url_secret_exists       = try(length(data.google_secret_manager_secrets.billing_server_url[0].secrets) > 0, false)
  billing_server_api_token_secret_exists = try(length(data.google_secret_manager_secrets.billing_server_api_token[0].secrets) > 0, false)

  dashboard_api_billing_server_url       = try(trimspace(data.google_secret_manager_secret_version.billing_server_url[0].secret_data), "")
  dashboard_api_billing_server_api_token = try(trimspace(data.google_secret_manager_secret_version.billing_server_api_token[0].secret_data), "")

  dashboard_api_ory_project_api_token = try(trimspace(data.google_secret_manager_secret_version.ory_project_api_key[0].secret_data), "")

  dashboard_api_ory_env_vars = {
    ORY_SDK_URL           = var.ory_sdk_url
    ORY_ISSUER_URL        = var.ory_issuer_url
    ORY_PROJECT_API_TOKEN = local.dashboard_api_ory_project_api_token
  }

  dashboard_api_env_vars = merge({
    GIN_MODE                               = "release"
    ENVIRONMENT                            = var.environment
    ADMIN_TOKEN                            = trimspace(data.google_secret_manager_secret_version.dashboard_api_admin_token.secret_data)
    POSTGRES_CONNECTION_STRING             = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    AUTH_DB_CONNECTION_STRING              = data.google_secret_manager_secret_version.postgres_connection_string.secret_data
    AUTH_DB_READ_REPLICA_CONNECTION_STRING = trimspace(data.google_secret_manager_secret_version.postgres_read_replica_connection_string.secret_data)
    CLICKHOUSE_CONNECTION_STRING           = local.clickhouse_connection_string
    # The Nomad jobspec template renders each entry as `${key} = "${value}"`,
    # so the embedded JSON's `"` characters must be pre-escaped to produce
    # valid HCL.
    AUTH_PROVIDER_CONFIG         = replace(jsonencode(local.auth_provider_config), "\"", "\\\"")
    REDIS_URL                    = local.redis_url
    REDIS_CLUSTER_URL            = local.redis_cluster_url
    REDIS_TLS_CA_BASE64          = trimspace(data.google_secret_manager_secret_version.redis_tls_ca_base64.secret_data)
    BILLING_SERVER_URL           = local.dashboard_api_billing_server_url
    BILLING_SERVER_API_TOKEN     = local.dashboard_api_billing_server_api_token
    OTEL_COLLECTOR_GRPC_ENDPOINT = "localhost:${local.otel_collector_grpc_port}"
    LOGS_COLLECTOR_ADDRESS       = "http://localhost:${local.logs_proxy_port}"
  }, local.dashboard_api_ory_env_vars, var.dashboard_api_env_vars)
}
