job "vault" {
  type        = "service"
  priority    = 90

  group "vault" {
    count = ${vault_server_count}

    constraint {
      operator = "distinct_hosts"
      value    = "true"
    }

    network {
      port "vault" {
        static = ${vault_port}
      }
      port "vault_cluster" {
        static = ${vault_cluster_port}
      }
    }

    service {
      name = "vault"
      port = "vault"

      check {
        type     = "http"
        protocol = "https"
        path     = "/v1/sys/health?standbyok=true" # return 200 for all nodes
        interval = "1s"
        timeout  = "2s"
        tls_skip_verify = "true"
        tls_server_name = "vault.service.consul"
      }
    }

    service {
      name = "vault-leader"
      port = "vault"
      tags = ["leader-only"]

      check {
        type            = "http"
        method          = "GET"
        path            = "/v1/sys/health"      # no standbyok, only leader returns 200
        interval        = "1s"
        timeout         = "2s"
        protocol        = "https"
        tls_skip_verify = "true"
        tls_server_name = "vault.service.consul"

        # dont block rollout on leader check
        # if you remove this, deployments will never go through because n-1 upstreams (correctly) fail
        on_update                = "ignore"

        # anti-flap
        success_before_passing = 2
        failures_before_critical = 2
      }
    }

    task "vault" {
      driver = "raw_exec"

      config {
        command = "/opt/vault/bin/vault"
        args = [
          "server",
          "-config=local/vault-config.hcl"
        ]
      }

      resources {
        memory_max = ${memory_max}
        memory     = ${memory}
        cpu        = ${cpu}
      }

      template {
        data = <<EOF
${vault_config}
EOF
        destination = "local/vault-config.hcl"
        change_mode = "restart"
      }

      %{ if consul_acl_token != "" }
      template {
        data = <<EOF
CONSUL_HTTP_TOKEN=${consul_acl_token}
EOF
        destination = "secrets/.env"
        env         = true
      }
      %{ endif }

      # Vault TLS Certificate
      template {
        data = <<EOF
${vault_tls_cert}
EOF
        destination = "local/vault.crt"
        perms       = 0644
        change_mode = "restart"
      }

      # Vault TLS Private Key
      template {
        data = <<EOF
${vault_tls_key}
EOF
        destination = "local/vault.key"
        perms       = 0600
        change_mode = "restart"
      }

      # Vault TLS CA Certificate
      template {
        data = <<EOF
${vault_tls_ca}
EOF
        destination = "local/ca.crt"
        perms       = 0644
        change_mode = "restart"
      }
    }

  }

  group "otel-collector" {
    count = 1

    constraint {
      operator = "distinct_hosts"
      value    = "true"
    }

    task "otel-collector" {
      driver = "docker"

      config {
        network_mode = "host"

        image = "otel/opentelemetry-collector-contrib:0.123.0"
        args = [
          "--config=local/otel.yaml",
          "--feature-gates=pkg.translator.prometheus.NormalizeName",
        ]
      }

      resources {
        cpu    = 250
        memory = 128
      }

      template {
        data        =<<EOF
${otel_agent_config}
EOF
        destination = "local/otel.yaml"
      }
    }
  }
}
