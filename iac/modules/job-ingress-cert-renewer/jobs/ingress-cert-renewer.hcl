job "ingress-http2-cert-renewer" {
  node_pool = "${node_pool}"
  priority  = 80

  group "ingress-http2-cert-renewer" {
    count = 1

    restart {
      attempts = 3
      delay    = "30s"
      interval = "10m"
      mode     = "delay"
    }

    task "renew" {
      driver = "docker"

      config {
        image        = "${image}"
        network_mode = "host"
        args         = ["/local/renew.sh"]
      }

      env {
        GCP_PROJECT_ID          = "${gcp_project_id}"
        CA_POOL                 = "${ca_pool}"
        CA_ID                   = "${ca_id}"
        CA_LOCATION             = "${ca_location}"
        SERVER_NAME             = "${server_name}"
        CERT_VALIDITY           = "${cert_validity}"
        RENEW_INTERVAL          = "${renew_interval}"
        CERTIFICATE_CONSUL_KEY  = "${certificate_consul_key}"
        PRIVATE_KEY_CONSUL_KEY  = "${private_key_consul_key}"
        CLIENT_CA_CONSUL_KEY    = "${client_ca_consul_key}"
        RELOAD_CONSUL_KEY       = "${reload_consul_key}"
        CONSUL_ENDPOINT         = "${consul_endpoint}"
      }

      template {
        data        = <<EOF
#!/bin/sh
set -eu

apk add --no-cache curl openssl >/dev/null

put_consul_key() {
  path="$${1}"
  file="$${2}"
  curl --fail --show-error --silent \
    --request PUT \
    --header "X-Consul-Token: $${CONSUL_TOKEN}" \
    --data-binary "@$${file}" \
    "$${CONSUL_ENDPOINT}/v1/kv/$${path}" >/dev/null
}

renew_once() {
  workdir="$(mktemp -d)"
  trap 'rm -rf "$${workdir}"' EXIT

  cert_id="grpc-api-http2-$(date -u +%Y%m%d%H%M%S)"

  gcloud privateca certificates create "$${cert_id}" \
    --project "$${GCP_PROJECT_ID}" \
    --issuer-pool "$${CA_POOL}" \
    --issuer-location "$${CA_LOCATION}" \
    --generate-key \
    --key-output-file "$${workdir}/tls.key" \
    --cert-output-file "$${workdir}/tls.crt" \
    --dns-san "$${SERVER_NAME}" \
    --use-preset-profile "leaf_server_tls" \
    --validity "$${CERT_VALIDITY}" \
    --quiet

  gcloud privateca roots describe "$${CA_ID}" \
    --project "$${GCP_PROJECT_ID}" \
    --location "$${CA_LOCATION}" \
    --pool "$${CA_POOL}" \
    --format="value(pemCaCertificates)" > "$${workdir}/client-ca.crt"

  put_consul_key "$${CERTIFICATE_CONSUL_KEY}" "$${workdir}/tls.crt"
  put_consul_key "$${PRIVATE_KEY_CONSUL_KEY}" "$${workdir}/tls.key"
  put_consul_key "$${CLIENT_CA_CONSUL_KEY}" "$${workdir}/client-ca.crt"
  printf '%s\n' "$${cert_id}" > "$${workdir}/reload"
  put_consul_key "$${RELOAD_CONSUL_KEY}" "$${workdir}/reload"
  rm -rf "$${workdir}"
  trap - EXIT
}

while true; do
  renew_once
  sleep "$${RENEW_INTERVAL}"
done
EOF
        destination = "local/renew.sh"
        perms       = "0555"
      }

      template {
        data        = <<EOF
CONSUL_TOKEN="${consul_token}"
EOF
        destination = "secrets/consul-token"
        env         = true
      }

      resources {
        cpu    = 100
        memory = 256
      }
    }
  }
}
