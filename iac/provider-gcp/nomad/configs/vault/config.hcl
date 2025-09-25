ui = false
disable_mlock = false

log_level = "info"
log_requests_level = "debug"

listener "tcp" {
  address       = "0.0.0.0:${vault_port}"
  tls_disable   = false
  tls_cert_file = "local/vault.crt"
  tls_key_file  = "local/vault.key"
  tls_client_ca_file = "local/ca.crt"

  telemetry {
    unauthenticated_metrics_access = true
  }

}

storage "spanner" {
  database   = "${spanner_database_path}"
  ha_enabled = "true"
}


seal "gcpckms" {
  project     = "${gcp_project_id}"
  region      = "${gcp_region}"
  key_ring    = "${kms_keyring}"
  crypto_key  = "${kms_crypto_key}"
}

api_addr = "https://{{ env "NOMAD_IP_vault" }}:${vault_port}"
cluster_addr = "https://{{ env "NOMAD_IP_vault_cluster" }}:${vault_cluster_port}"

telemetry {
  prometheus_retention_time = "24h"
  disable_hostname = true
}
