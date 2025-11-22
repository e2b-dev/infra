job "otel-collector" {
  datacenters = ["${datacenter}"]
  type        = "system"
  node_pool   = "${node_pool}"

  group "otel-collector" {
    task "otel" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "otel/opentelemetry-collector:0.101.0"
        args = [
          "--config", "local/config/otel-collector-config.yaml"
        ]
      }

      resources {
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        NODE_ID = "$${node.unique.name}"
      }

      template {
        data        =  <<EOF
${otel_collector_config}
EOF
        destination = "local/config/otel-collector-config.yaml"
      }
    }
  }
}