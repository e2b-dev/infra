job "loki" {
  datacenters = ["${datacenter}"]
  node_pool   = "${node_pool}"
  type        = "service"

  priority = 75

  group "loki-service" {
    restart {
      interval = "5s"
      attempts = 1
      delay    = "5s"
      mode     = "delay"
    }

    network {
      port "${loki_service_port_name}" { to = "${loki_service_port_number}" }
    }

    service {
      name = "loki"
      port = "${loki_service_port_name}"
      check {
        type     = "http"
        path     = "/ready"
        interval = "20s"
        timeout  = "2s"
        port     = "${loki_service_port_name}"
      }
    }

    task "loki" {
      driver = "docker"
      config {
        network_mode = "host"
%{ if docker_image_prefix != "" }
        image = "${docker_image_prefix}/grafana/loki:2.9.8"
%{ else }
        image = "grafana/loki:2.9.8"
%{ endif }
        args  = ["-config.file","local/loki-config.yml"]
      }

      resources {
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      template {
        data = <<EOF
auth_enabled: false
server:
  http_listen_port: ${loki_service_port_number}
  log_level: "warn"
common:
  path_prefix: /loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
storage_config:
  filesystem:
    directory: /loki/data
chunk_store_config:
  chunk_cache_config:
    embedded_cache:
      enabled: true
      max_size_mb: 512
      ttl: 30m
schema_config:
  configs:
    - from: 2024-03-05
      store: tsdb
      object_store: filesystem
      schema: v12
      index:
        prefix: loki_index_
        period: 24h
limits_config:
  retention_period: 168h
EOF
        destination = "local/loki-config.yml"
      }
    }
  }
}
