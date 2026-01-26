job "client-proxy" {
  datacenters = ["${datacenter}"]
  node_pool   = "${node_pool}"

  priority = 80

  group "client-proxy" {
    restart {
      attempts = 2
      interval = "10m"
      delay    = "10s"
      mode     = "fail"
    }

    reschedule {
      delay          = "30s"
      delay_function = "exponential"
      max_delay      = "10m"
      unlimited      = true
    }

    count = ${count}

    constraint {
      operator  = "distinct_hosts"
      value     = "true"
    }

    network {
      port "${proxy_port_name}" {
        static = "${proxy_port}"
      }

      port "${api_port_name}" {
        static = "${api_port}"
      }
    }

    service {
      name = "proxy"
      port = "${proxy_port_name}"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.edge-proxy.rule=HostRegexp(`.+\\.${domain_name}`)",
        "traefik.http.routers.edge-proxy.entrypoints=websecure",
        "traefik.http.routers.edge-proxy.tls=true",
        "traefik.http.routers.edge-proxy.tls.domains[0].main=${domain_name}",
        "traefik.http.routers.edge-proxy.tls.domains[0].sans=*.${domain_name}",
        "traefik.http.routers.edge-proxy.priority=1",
        "traefik.http.routers.edge-proxy.service=edge-proxy",
        "traefik.http.services.edge-proxy.loadbalancer.server.port=${proxy_port}",
        "traefik.http.routers.edge-execute.rule=Host(`edge.${domain_name}`) && PathPrefix(`/execute`)",
        "traefik.http.routers.edge-execute.entrypoints=websecure",
        "traefik.http.routers.edge-execute.tls=true",
        "traefik.http.routers.edge-execute.priority=200",
        "traefik.http.routers.edge-execute.service=edge-proxy",
        "traefik.http.services.edge-execute.loadbalancer.server.port=${proxy_port}"
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "/health/traffic"
        interval = "3s"
        timeout  = "3s"
        port     = "${api_port_name}"
      }
    }

    service {
      name = "edge-api"
      port = "${api_port_name}"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.edge.rule=Host(`edge.${domain_name}`)",
        "traefik.http.routers.edge.entrypoints=websecure",
        "traefik.http.routers.edge.tls=true",
        "traefik.http.routers.edge.priority=100",
        "traefik.http.routers.edge.service=edge-api",
        "traefik.http.services.edge-api.loadbalancer.server.port=${api_port}"
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "3s"
        timeout  = "3s"
        port     = "${api_port_name}"
      }
    }

%{ if update_stanza }
    update {
      max_parallel     = ${update_max_parallel}
      canary           = ${update_max_parallel}
      min_healthy_time = "10s"
      healthy_deadline = "30s"
      auto_promote     = true
      progress_deadline = "24h"
    }
%{ endif }

    task "start" {
      driver = "docker"
%{ if update_stanza }
      kill_timeout = "24h"
%{ endif }
      kill_signal  = "SIGTERM"

      resources {
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        NODE_ID = "$${node.unique.id}"
        NODE_IP = "$${attr.unique.network.ip-address}"

        EDGE_PORT         = "${api_port}"
        EDGE_SECRET       = "${api_secret}"
        PROXY_PORT        = "${proxy_port}"
        ORCHESTRATOR_PORT = "${orchestrator_port}"

        SD_ORCHESTRATOR_PROVIDER         = "NOMAD"
        SD_ORCHESTRATOR_NOMAD_ENDPOINT   = "${nomad_endpoint}"
        SD_ORCHESTRATOR_NOMAD_TOKEN      = "${nomad_token}"
        SD_ORCHESTRATOR_NOMAD_JOB_PREFIX = "template-manager"

        SD_EDGE_PROVIDER             = "NOMAD"
        SD_EDGE_NOMAD_ENDPOINT       = "${nomad_endpoint}"
        SD_EDGE_NOMAD_TOKEN          = "${nomad_token}"
        SD_EDGE_NOMAD_JOB_PREFIX     = "client-proxy"

        ENVIRONMENT = "${environment}"

        OTEL_COLLECTOR_GRPC_ENDPOINT  = "${otel_collector_grpc_endpoint}"
        LOGS_COLLECTOR_ADDRESS        = "${logs_collector_address}"
        REDIS_URL                     = "${redis_url}"
        REDIS_CLUSTER_URL             = "${redis_cluster_url}"
        REDIS_SECURE_CLUSTER_URL      = "${redis_secure_cluster_url}"
        REDIS_TLS_CA_BASE64           = "${redis_tls_ca_base64}"
        LOKI_URL                      = "${loki_url}"

        %{ if launch_darkly_api_key != "" }
        LAUNCH_DARKLY_API_KEY         = "${launch_darkly_api_key}"
        %{ endif }
        E2B_DEBUG="true"
      }

      config {
        network_mode = "host"
        image        = "${image_name}"
        ports        = ["${proxy_port_name}", "${api_port_name}"]
      }
    }
  }
}
