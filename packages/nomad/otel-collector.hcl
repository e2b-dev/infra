job "otel-collector" {
  datacenters = ["${gcp_zone}"]
  type        = "system"
  node_pool   = "all"

  priority = 95

  group "otel-collector" {
    network {
      port "health" {
        to = 13133
      }

      port "metrics" {
        to = 8888
      }

      # Receivers
      port "grpc" {
        to = 4317
      }

      port "http" {
        to = 4318
      }
    }

    service {
      name = "otel-collector"
      port = "grpc"
      tags = ["grpc"]

      check {
        type     = "http"
        name     = "health"
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = 13133
      }
    }

    task "start-collector" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "otel/opentelemetry-collector-contrib:0.99.0"

        volumes = [
          "local/config:/config",
          "/var/log/session-proxy:/var/log/session-proxy",
        ]
        args = [
          "--config=local/config/otel-collector-config.yaml",
          "--feature-gates=pkg.translator.prometheus.NormalizeName",
        ]

        ports = [
          "metrics",
          "grpc",
          "health",
          "http",
        ]
      }

      resources {
        memory_max = 4096
        memory = 1024
        cpu    = 256
      }

      template {
        data = <<EOF
receivers:
  otlp:
    protocols:
      grpc:
        max_recv_msg_size_mib: 100
        read_buffer_size: 10943040
        max_concurrent_streams: 200
        write_buffer_size: 10943040
  prometheus:
    config:
      scrape_configs:
        - job_name: nomad
          scrape_interval: 15s
          scrape_timeout: 5s
          metrics_path: '/v1/metrics'
          static_configs:
            - targets: ['localhost:4646']
          params:
            format: ['prometheus']
          consul_sd_configs:
          - services: ['nomad-client', 'nomad', 'api', 'client-proxy', 'session-proxy', 'otel-collector', 'logs-collector', 'docker-reverse-proxy', 'loki', 'orchestrator', 'template-manager']
            token: "${consul_token}"

          relabel_configs:
          - source_labels: ['__meta_consul_tags']
            regex: '(.*)http(.*)'
            action: keep

processors:
  batch:
    timeout: 5s
  filter:
    metrics:
      include:
        match_type: regexp
        # Exclude metrics that start with `http`, `go`, `rpc`, or `nomad` but aren't `nomad.client`
        metric_names:
          - "nomad_client_host_cpu_idle"
          - "nomad_client_host_disk_available"
          - "nomad_client_host_disk_size"
          - "nomad_client_allocs_memory_usage"
          - "nomad_client_allocs_cpu_usage"
          - "nomad_client_host_memory_available"
          - "nomad_client_host_memory_total"
          - "nomad_client_unallocated_memory"
          - "nomad_nomad_job_summary_running"
          - "orchestrator.*"
          - "api.*"
          - "client_proxy.*"
  metricstransform:
    transforms:
      - include: "nomad_client_host_cpu_idle"
        match_type: strict
        action: update
        operations:
          - action: aggregate_labels
            aggregation_type: sum
            label_set: [instance, node_id, node_status, node_pool]
  attributes/session-proxy:
    actions:
      - key: service.name
        action: upsert
        value: session-proxy
extensions:
  basicauth/grafana_cloud:
    # https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/extension/basicauthextension
    client_auth:
      username: "${grafana_username}"
      password: "${grafana_otel_collector_token}"

  health_check:

exporters:
  debug:
    verbosity: detailed
  otlphttp/grafana_cloud:
    # https://github.com/open-telemetry/opentelemetry-collector/tree/main/exporter/otlpexporter
    endpoint: "${grafana_otlp_url}/otlp"
    auth:
      authenticator: basicauth/grafana_cloud

service:
  telemetry:
    logs:
      level: warn
  extensions:
    - basicauth/grafana_cloud
    - health_check
  pipelines:
    metrics:
      receivers:
        - prometheus
        - otlp
      processors: [filter, batch, metricstransform]
      exporters:
        - otlphttp/grafana_cloud
    traces:
      receivers:
        - otlp
      processors: [batch]
      exporters:
        - otlphttp/grafana_cloud
    logs:
      receivers:
      # - filelog/session-proxy
        - otlp
      processors: [batch]
      exporters:
        - otlphttp/grafana_cloud
EOF

        destination = "local/config/otel-collector-config.yaml"
      }
    }
  }
}
