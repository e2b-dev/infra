resource "nomad_job" "ingress_cert_renewer" {
  jobspec = templatefile("${path.module}/jobs/ingress-cert-renewer.hcl", {
    node_pool = var.node_pool

    image = var.image

    gcp_project_id = var.gcp_project_id
    ca_pool        = var.ca_pool
    ca_id          = var.ca_id
    ca_location    = var.ca_location
    server_name    = var.server_name
    cert_validity  = var.cert_validity
    renew_interval = var.renew_interval

    certificate_consul_key = var.certificate_consul_key
    private_key_consul_key = var.private_key_consul_key
    client_ca_consul_key   = var.client_ca_consul_key
    reload_consul_key      = var.reload_consul_key

    consul_endpoint = var.consul_endpoint
    consul_token    = var.consul_token
  })
}
