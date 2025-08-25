job "vault" {
  datacenters = ["${gcp_zone}"]
  node_pool   = "default"
  type        = "service"
  priority    = 90

  group "vault" {
    count = ${vault_server_count}

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
        path     = "/v1/sys/health?standbyok=true&uninitcode=200"
        interval = "1s"
        timeout  = "2s"
      }
    }

    task "vault" {
      driver = "raw_exec"

      config {
        command = "vault"
        args = [
          "server",
          "-config=local/vault-config.hcl"
        ]
      }

      env {
        VAULT_LOG_LEVEL = "info"
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
    }
  }
}
