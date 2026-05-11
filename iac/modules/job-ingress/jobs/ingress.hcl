job "ingress" {
  node_pool = "${node_pool}"
  priority  = 90

  group "ingress" {
    count = ${count}

    constraint {
      operator  = "distinct_hosts"
      value     = "true"
    }

    network {
      port "ingress" {
        static = "${ingress_port}"
      }

      port "ingress-http2" {
        static = "${ingress_http2_port}"
      }

      port "control" {
        static = "${control_port}"
      }
    }

# https://developer.hashicorp.com/nomad/docs/job-specification/update
%{ if update_stanza }
    update {
      # The number of instances that can be updated at the same time
      max_parallel     = 1
      # Number of extra instances that can be spawn before killing the old one
      canary           = 1
      # Time to wait for the canary to be healthy
      min_healthy_time = "10s"
      # Give TLS-backed ingress enough time to wait for initial cert material.
      healthy_deadline = "5m"
      # Whether to promote the canary if the rest of the group is not healthy
      auto_promote     = true
      # Deadline for the update to be completed
      progress_deadline = "24h"
    }
%{ endif }

    service {
      port = "ingress"
      name = "ingress"
      task = "ingress"

      check {
        type     = "http"
        name     = "health"
        path     = "/ping"
        interval = "3s"
        timeout  = "3s"
        port     = "${ingress_port}"
      }
    }

    # Expose Nomad dashboard and API via Traefik ingress
    service {
      name = "ingress-dashboard"
      port = "control"
      task = "ingress"

      tags = [
        "traefik.enable=true",

        "traefik.http.routers.traefik.rule=PathPrefix(`/dashboard`) || PathPrefix(`/api`)",
        "traefik.http.routers.traefik.entrypoints=traefik",
        "traefik.http.routers.traefik.service=api@internal",
      ]
    }

    task "ingress" {
      driver = "docker"

      %{ if update_stanza }
        kill_timeout = "24h"
      %{ endif }
      kill_signal  = "SIGTERM"

      config {
        network_mode = "host"
        image        = "traefik:v3.5"
        ports        = ["control", "ingress", "ingress-http2"]
        args = [
          "--configFile=/local/traefik.toml",
        ]
      }

      template {
        data        = <<EOF
${traefik_config}
EOF
        destination = "local/traefik.toml"
      }

      template {
        data = "# content ignored, ensures the directory exists"
        destination = "local/config/.keep"
      }

%{ if ingress_http2_tls != null }
      template {
        data = "# content ignored, ensures the directory exists"
        destination = "local/tls/.keep"
      }

      template {
        data        = <<EOF
{{ key "${ingress_http2_tls.certificate_consul_key}" }}
EOF
        destination = "local/tls/http2.crt"
        perms       = "0444"
        change_mode = "${ingress_http2_tls.reload_consul_key == null ? "restart" : "noop"}"
        error_on_missing_key = true
      }

      template {
        data        = <<EOF
{{ key "${ingress_http2_tls.private_key_consul_key}" }}
EOF
        destination = "secrets/tls/http2.key"
        perms       = "0400"
        change_mode = "${ingress_http2_tls.reload_consul_key == null ? "restart" : "noop"}"
        error_on_missing_key = true
      }

%{ if ingress_http2_tls.require_client_certificate }
      template {
        data        = <<EOF
{{ key "${ingress_http2_tls.client_ca_consul_key}" }}
EOF
        destination = "local/tls/client-ca.crt"
        perms       = "0444"
        change_mode = "${ingress_http2_tls.reload_consul_key == null ? "restart" : "noop"}"
        error_on_missing_key = true
      }
%{ endif }
%{ if ingress_http2_tls.reload_consul_key != null }
      template {
        data        = <<EOF
{{ key "${ingress_http2_tls.reload_consul_key}" }}
EOF
        destination = "local/tls/http2.reload"
        perms       = "0444"
        change_mode = "restart"
        error_on_missing_key = true
      }
%{ endif }
%{ endif }

%{ for filename, content in config_files }
      template {
        data        = <<EOF
${content}
EOF
        destination = "local/config/${filename}"
      }
%{ endfor }

      resources {
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }
    }
  }
}
