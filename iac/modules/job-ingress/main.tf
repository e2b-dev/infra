locals {
  ping_config = <<-EOF
[http.routers.ping-web]
  rule = "Path(`/ping`)"
  entryPoints = ["web"]
  service = "ping@internal"
  priority = 10000

[http.routers.ping-websecure]
  rule = "Path(`/ping`)"
  entryPoints = ["websecure"]
  service = "ping@internal"
  priority = 10000
  [http.routers.ping-websecure.tls]
EOF

  tls_config = var.ingress_http2_tls == null ? null : <<-EOF
[[tls.certificates]]
  certFile = "/local/tls/http2.crt"
  keyFile = "/secrets/tls/http2.key"

%{if var.ingress_http2_tls.require_client_certificate~}
[tls.options.gcp-lb-mtls.clientAuth]
  caFiles = ["/local/tls/client-ca.crt"]
  clientAuthType = "RequireAndVerifyClientCert"
%{endif~}
EOF

  traefik_config = templatefile("${path.module}/jobs/traefik.toml", {
    ingress_port       = var.ingress_proxy_port
    ingress_http2_port = var.ingress_http2_proxy_port
    control_port       = var.ingress_control_port

    nomad_endpoint = var.nomad_endpoint
    nomad_token    = var.nomad_token

    consul_endpoint = var.consul_endpoint
    consul_token    = var.consul_token

    otel_collector_grpc_endpoint = var.otel_collector_grpc_endpoint
  })
}

resource "nomad_job" "ingress" {
  jobspec = templatefile("${path.module}/jobs/ingress.hcl", {
    count         = var.ingress_count
    node_pool     = var.node_pool
    update_stanza = var.update_stanza
    cpu_count     = var.ingress_cpu_count
    memory_mb     = var.ingress_memory_mb

    ingress_port       = var.ingress_proxy_port
    ingress_http2_port = var.ingress_http2_proxy_port
    control_port       = var.ingress_control_port

    traefik_config    = local.traefik_config
    ingress_http2_tls = var.ingress_http2_tls

    config_files = merge(
      var.traefik_config_files,
      { "ping.toml" = local.ping_config },
      local.tls_config == null ? {} : { "tls.toml" = local.tls_config },
    )
  })
}
