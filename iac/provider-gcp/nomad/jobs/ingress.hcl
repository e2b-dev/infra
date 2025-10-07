job "ingress" {
  datacenters = ["${gcp_zone}"]
  node_pool   = "${node_pool}"
  priority    = 90

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

      port "control" {
        static = "${control_port}"
      }
    }

# https://developer.hashicorp.com/nomad/docs/job-specification/update
%{ if update_stanza }
    update {
      max_parallel = 1    # Update only 1 node at a time
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

    task "ingress" {
      driver = "docker"

      # If we need more than 30s we will need to update the max_kill_timeout in nomad
      # https://developer.hashicorp.com/nomad/docs/configuration/client#max_kill_timeout
      %{ if update_stanza }
        kill_timeout = "24h"
      %{ endif }

      kill_signal  = "SIGTERM"

      config {
        network_mode = "host"
        image        = "traefik:v3.5"
        ports        = ["control", "ingress"]
        args = [
          # Entry-points that are set internally by Traefik
          "--entrypoints.web.address=:${ingress_port}",
          "--entrypoints.traefik.address=:${control_port}",

          # Traefik internals (logging, metrics, ...)
          "--api.dashboard=true",
          "--api.insecure=false",

          "--accesslog=true",
          "--ping=true",
          "--ping.entryPoint=web",
          "--metrics=true",
          "--metrics.prometheus=true",
          "--metrics.prometheus.entryPoint=traefik",

          # Traefik Nomad provider
          "--providers.nomad=true",
          "--providers.nomad.endpoint.address=${nomad_endpoint}",
          "--providers.nomad.endpoint.token=${nomad_token}",

          # Traefik Consul provider
          "--providers.consulcatalog=true",
          "--providers.consulcatalog.exposedByDefault=false",
          "--providers.consulcatalog.endpoint.address=${consul_endpoint}",
          "--providers.consulcatalog.endpoint.token=${consul_token}",
        ]
      }

      resources {
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }
    }
  }
}