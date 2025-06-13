job "otel-collector" {
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
        image        = "otel/opentelemetry-collector-contrib:0.123.0"

        volumes = [
          "local/config:/config",
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
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
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
          - services: ['nomad-client', 'nomad', 'api', 'client-proxy', 'otel-collector', 'logs-collector', 'docker-reverse-proxy', 'loki', 'orchestrator', 'template-manager']
            token: "${consul_token}"

          relabel_configs:
          - source_labels: ['__meta_consul_tags']
            regex: '(.*)http(.*)'
            action: keep

  prometheus/clickhouse:
    config:
      scrape_configs:
        - job_name: clickhouse
          scrape_interval: 30s
          metrics_path: '/metrics'
          consul_sd_configs:
          - services: ['clickhouse']
            token: "${consul_token}"
          relabel_configs:
          - source_labels: [__address__]
            regex: '(.*):9000'
            replacement: $1:9363
            target_label: __address__

processors:
  batch:
    timeout: 5s

  batch/clickhouse:
    timeout: 5s
    send_batch_size: 100000

  # keep only metrics that are used
  filter:
    metrics:
      include:
        match_type: regexp
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
      exclude:
        match_type: regexp
        # Exclude `rpc.server.duration` as it's processed in `filter/rpc_duration_only`
        metric_names:
          - "rpc.server.duration.*"

  filter/clickhouse:
    metrics:
      include:
        match_type: strict
        metric_names:
          # ──────  Query load & latency ──────
          - ClickHouseProfileEvents_SelectQuery
          - ClickHouseProfileEvents_FailedSelectQuery
          - ClickHouseProfileEvents_SelectQueryTimeMicroseconds
          - ClickHouseProfileEvents_InsertQuery
          - ClickHouseProfileEvents_FailedInsertQuery
          - ClickHouseProfileEvents_InsertQueryTimeMicroseconds
          - ClickHouseProfileEvents_QueryTimeMicroseconds
          - ClickHouseProfileEvents_Query
          - ClickHouseMetrics_Query
          - ClickHouseProfileEvents_QueryMemoryLimitExceeded

          # ──────  Table stats ──────
          - ClickHouseAsyncMetrics_TotalRowsOfMergeTreeTables
          - ClickHouseAsyncMetrics_TotalPartsOfMergeTreeTables
          - ClickHouseAsyncMetrics_TotalBytesOfMergeTreeTables

          # ──────  Read / write throughput ──────
          - ClickHouseProfileEvents_AsyncInsertBytes
          - ClickHouseProfileEvents_AsyncInsertRows
          - ClickHouseProfileEvents_InsertedBytes
          - ClickHouseProfileEvents_InsertedRows
          - ClickHouseProfileEvents_SelectedBytes
          - ClickHouseProfileEvents_SelectedRows
          - ClickHouseProfileEvents_SlowRead

          # ──────  Memory ──────
          - ClickHouseAsyncMetrics_CGroupMemoryUsed
          - ClickHouseAsyncMetrics_CGroupMemoryTotal

          # ──────  Network ──────
          - ClickHouseMetrics_NetworkSend
          - ClickHouseMetrics_NetworkReceive

          # ──────  Disk / S3 traffic ──────
          - ClickHouseAsyncMetrics_DiskTotal_default
          - ClickHouseAsyncMetrics_DiskAvailable_default
          - ClickHouseAsyncMetrics_DiskUsed_default
          - ClickHouseProfileEvents_S3GetObject
          - ClickHouseProfileEvents_S3PutObject
          - ClickHouseProfileEvents_ReadBufferFromS3Bytes
          - ClickHouseProfileEvents_WriteBufferFromS3Bytes

          # ──────  Connections ──────
          - ClickHouseMetrics_TCPConnection
          - ClickHouseMetrics_HTTPConnectionsTotal


  filter/external_metrics:
    metrics:
      include:
        match_type: regexp
        metric_names:
          - "e2b.*"

  metricstransform:
    transforms:
      - include: "nomad_client_host_cpu_idle"
        match_type: strict
        action: update
        operations:
          - action: aggregate_labels
            aggregation_type: sum
            label_set: [instance, node_id, node_status, node_pool]

  filter/rpc_duration_only:
    metrics:
      include:
        match_type: regexp
        # Include info about grpc server endpoint durations - used for monitoring request times
        metric_names:
          - "rpc.server.duration.*"
  resource/remove_instance:
    attributes:
      - action: delete
        key: service.instance.id
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
  clickhouse:
    endpoint: tcp://${clickhouse_host}:${clickhouse_port}
    database: ${clickhouse_database}
    username: ${clickhouse_username}
    password: ${clickhouse_password}
    async_insert: true
    create_schema: false
    metrics_tables:
      gauge:
        name: "metrics_gauge"
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
    metrics/rpc_only:
      receivers:
        - prometheus
        - otlp
      processors: [filter/rpc_duration_only, resource/remove_instance, batch]
      exporters:
        - otlphttp/grafana_cloud
    metrics/clickhouse:
      receivers:  [prometheus/clickhouse]
      processors: [filter/clickhouse, batch]
      exporters:
        - otlphttp/grafana_cloud
    metrics/external:
      receivers:  [otlp]
      processors: [filter/external_metrics, batch/clickhouse]
      exporters:  [clickhouse]
    traces:
      receivers:
        - otlp
      processors: [batch]
      exporters:
        - otlphttp/grafana_cloud
    logs:
      receivers:
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