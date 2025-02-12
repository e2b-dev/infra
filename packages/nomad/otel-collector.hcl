variable "gcp_zone" {
  type = string
}

variable "grafana_api_key" {
  type = string
}

variable "grafana_logs_username" {
  type = string
}

variable "grafana_traces_username" {
  type = string
}

variable "grafana_metrics_username" {
  type = string
}

variable "grafana_logs_endpoint" {
  type = string
}

variable "grafana_traces_endpoint" {
  type = string
}

variable "grafana_metrics_endpoint" {
  type = string
}

variable "consul_token" {
  type = string
}

variables {
  otel_image = "otel/opentelemetry-collector-contrib:0.99.0"
}

job "otel-collector" {
  datacenters = [var.gcp_zone]
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
        image        = var.otel_image

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
      # http:
  # nginx/session-proxy:
  #   endpoint: http://session-proxy.service.consul:3004/status
  #   collection_interval: 10s
  # filelog/session-proxy:
  #   include:
  #     - /var/log/session-proxy/access.log
  #   operators:
  #     - type: json_parser
  #       timestamp:
  #         parse_from: attributes.time
  #         layout: '%Y-%m-%dT%H:%M:%S%j'
  #     - type: remove
  #       id: body
  #       field: body
  #   resource:
  #     service.name: session-proxy
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
            token: "${var.consul_token}"

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
  basicauth/grafana_cloud_traces:
    client_auth:
      username: "${var.grafana_traces_username}"
      password: "${var.grafana_api_key}"
  basicauth/grafana_cloud_metrics:
    client_auth:
      username: "${var.grafana_metrics_username}"
      password: "${var.grafana_api_key}"
  basicauth/grafana_cloud_logs:
    client_auth:
      username: "${var.grafana_logs_username}"
      password: "${var.grafana_api_key}"
  health_check:

exporters:
  debug:
    verbosity: detailed
  otlp/grafana_cloud_traces:
    endpoint: "${var.grafana_traces_endpoint}"
    auth:
      authenticator: basicauth/grafana_cloud_traces
  loki/grafana_cloud_logs:
    endpoint: "${var.grafana_logs_endpoint}/loki/api/v1/push"
    auth:
      authenticator: basicauth/grafana_cloud_logs
  prometheusremotewrite/grafana_cloud_metrics:
    endpoint: "${var.grafana_metrics_endpoint}"
    auth:
      authenticator: basicauth/grafana_cloud_metrics

service:
  telemetry:
    logs:
      level: warn
  extensions:
    - basicauth/grafana_cloud_traces
    - basicauth/grafana_cloud_metrics
    - basicauth/grafana_cloud_logs
    - health_check
  pipelines:
    metrics:
      receivers:
        - prometheus
        - otlp
      processors: [filter, batch, metricstransform]
      exporters:
        - prometheusremotewrite/grafana_cloud_metrics
    # metrics/session-proxy:
    #   receivers:
        # - nginx/session-proxy
      # processors: [batch, attributes/session-proxy]
      # exporters:
      #   - prometheusremotewrite/grafana_cloud_metrics
    traces:
      receivers:
        - otlp
      processors: [batch]
      exporters:
        - otlp/grafana_cloud_traces
    logs:
      receivers:
      # - filelog/session-proxy
        - otlp
      processors: [batch]
      exporters:
        - loki/grafana_cloud_logs
EOF

        destination = "local/config/otel-collector-config.yaml"
      }
    }
  }
}
