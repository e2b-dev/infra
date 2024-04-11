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
  otel_image = "otel/opentelemetry-collector-contrib:0.90.1"
}

job "otel-collector" {
  datacenters = [var.gcp_zone]
  type        = "service"

  priority = 95

  group "collector" {
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
        memory_max = 2048
        memory = 512
        cpu    = 512
      }

      template {
        data = <<EOF
receivers:
  otlp:
    protocols:
      grpc:
      http:
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

extensions:
%{ if var.grafana_api_key != " " }
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
%{ endif }
  health_check:

exporters:
  debug:
    verbosity: detailed
%{ if var.grafana_api_key != " " }
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
%{ endif }

service:
  telemetry:
    logs:
      level: warn
  extensions:
    - health_check
%{ if var.grafana_api_key != " " }
    - basicauth/grafana_cloud_traces
    - basicauth/grafana_cloud_metrics
    - basicauth/grafana_cloud_logs
%{ endif }
  pipelines:
    metrics:
      receivers:
        - prometheus
        - otlp
      processors: [batch]
%{ if var.grafana_api_key != " " }
      exporters:
        - prometheusremotewrite/grafana_cloud_metrics
%{ else }
      exporters:
        - debug
%{ endif }
    traces:
      receivers:
        - otlp
      processors: [batch]
%{ if var.grafana_api_key != " " }
      exporters:
        - otlp/grafana_cloud_traces
%{ else }
      exporters:
        - debug
%{ endif }
    logs:
      receivers:
        - otlp
      processors: [batch]
%{ if var.grafana_api_key != " " }
      exporters:
        - loki/grafana_cloud_logs
%{ else }
      exporters:
        - debug
%{ endif }
EOF

        destination = "local/config/otel-collector-config.yaml"
      }
    }
  }
}
