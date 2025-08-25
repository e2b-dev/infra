ui = true
disable_mlock = false

listener "tcp" {
  address       = "0.0.0.0:${vault_port}"
  tls_disable   = true
}

storage "gcs" {
  bucket     = "${gcs_bucket_name}"
  ha_enabled = "true"
}

seal "gcpckms" {
  project     = "${gcp_project_id}"
  region      = "${gcp_region}"
  key_ring    = "${kms_keyring}"
  crypto_key  = "${kms_crypto_key}"
}

api_addr = "http://{{ env "NOMAD_IP_vault" }}:${vault_port}"
cluster_addr = "http://{{ env "NOMAD_IP_vault_cluster" }}:${vault_cluster_port}"

telemetry {
  prometheus_retention_time = "0s"
  disable_hostname = true
}
