job "ingress" {
  datacenters = ["${datacenter}"]
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

%{ if update_stanza }
    update {
      max_parallel     = 1
      canary           = 1
      min_healthy_time = "10s"
      healthy_deadline = "30s"
      auto_promote     = true
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

    task "ingress" {
      driver = "docker"

      %{ if update_stanza }
        kill_timeout = "24h"
      %{ endif }
      kill_signal  = "SIGTERM"

      config {
        network_mode = "host"
        image        = "traefik:v3.5"
        ports        = ["control", "ingress"]
        args = [
          "--entrypoints.websecure.address=:${ingress_port}",
          "--entrypoints.traefik.address=:${control_port}",

          "--api.dashboard=true",
          "--api.insecure=true",

          "--accesslog=true",
          "--ping=true",
          "--ping.entryPoint=websecure",
          "--metrics=true",
          "--metrics.prometheus=true",
          "--metrics.prometheus.entryPoint=traefik",

          "--providers.nomad=true",
          "--providers.nomad.endpoint.address=${nomad_endpoint}",
          "--providers.nomad.endpoint.token=${nomad_token}",
          "--providers.nomad.exposedByDefault=false",

          "--providers.consulcatalog=true",
          "--providers.consulcatalog.exposedbydefault=false",
          "--providers.consulcatalog.endpoint.address=${consul_endpoint_host}",
          "--providers.consulcatalog.endpoint.scheme=http",
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