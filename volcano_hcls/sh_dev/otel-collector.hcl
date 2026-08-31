variable "memory_mb" {
  type    = number
  default = 4096 // 增加内存限制，确保足够的资源
}

variable "cpu_count" {
  type    = number
  default = 2 // 增加CPU限制
}

variable "memory_mb_max" {
  type    = number
  default = 6144 // memory_mb * 1.5 的预计算值
}

locals {
  # memory_limiter: 75% hard limit, 20% spike
  # 生产环境只改 memory_mb，这里自动适配
  otel_memory_limit_mib = floor(var.memory_mb * 0.75)
  otel_memory_spike_mib = floor(var.memory_mb * 0.20)
}

job "otel-collector" {
  type      = "system"
  node_pool = "all"

  priority = 95

  group "otel-collector" {

    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval = "5s"
      attempts = 1
      delay    = "5s"
      mode     = "delay"
    }

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
        image        = "mp-bp-cn-shanghai.cr.volces.com/e2b/otel/opentelemetry-collector-contrib:0.146.0"

        volumes = [
          "local/config:/config",
          "/:/hostfs:ro",
        ]
        args = [
          "--config=local/config/otel-collector-config.yaml",
        ]

        ports = [
          "metrics",
          "grpc",
          "health",
          "http",
        ]
      }

      resources {
        memory_max = var.memory_mb_max
        memory     = var.memory_mb
        cpu        = var.cpu_count * 1000
      }

      env {
        NODE_ID          = "${node.unique.name}"
        MEMORY_LIMIT_MIB = "${local.otel_memory_limit_mib}"
        MEMORY_SPIKE_MIB = "${local.otel_memory_spike_mib}"
      }

      template {
        data        = <<EOF
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
        max_recv_msg_size_mib: 100
        max_concurrent_streams: 256  # 2万 sbx 并发量大，提高并发流上限
      http:
        endpoint: 0.0.0.0:4318

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

  hostmetrics:
    collection_interval: 30s
    scrapers:
      cpu:
        metrics:
          system.cpu.time:
            enabled: false
          system.cpu.utilization:
            enabled: false
          system.cpu.logical.count:
            enabled: true
          system.cpu.physical.count:
            enabled: true

      load:
        metrics:
          system.cpu.load_average.1m:
            enabled: true
          system.cpu.load_average.5m:
            enabled: true
          system.cpu.load_average.15m:
            enabled: false

      network:
        metrics:
          system.network.connections:
            enabled: false
          system.network.dropped:
            enabled: false
          system.network.errors:
            enabled: false
          system.network.io:
              enabled: true
          system.network.packets:
            enabled: false

      memory:
        metrics:
          system.linux.memory.dirty:
            enabled: false
          system.linux.memory.available:
            enabled: true
          system.memory.limit:
            enabled: true
          system.memory.page_size:
            enabled: false
          system.memory.usage:
            enabled: true
          system.memory.utilization:
            enabled: true

      filesystem:
        metrics:
          system.filesystem.inodes.usage:
            enabled: false
          system.filesystem.usage:
            enabled: true

processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: {{ env "MEMORY_LIMIT_MIB" }}
    spike_limit_mib: {{ env "MEMORY_SPIKE_MIB" }}

  # metrics 用：2s 收集窗口，单批不超过 8000 条
  batch:
    timeout: 2s
    send_batch_size: 4000
    send_batch_max_size: 8000

  # clickhouse 写入用：大批次提升写入效率
  batch/clickhouse:
    timeout: 5s
    send_batch_size: 10000
    send_batch_max_size: 20000

  filter/drop_by_device:
    error_mode: ignore
    metrics:
      datapoint:
        - 'metric.name == "system.network.io" and IsMatch(attributes["device"], "^(veth-.*|docker.*|lo)$")'
        - 'metric.name == "system.filesystem.usage" and IsMatch(attributes["device"], "^/dev/loop.*$")'

  attributes/strip_fs_labels:
    include:
      match_type: strict
      metric_names: [system.filesystem.usage]
    actions:
      - action: delete
        key: mode
      - action: delete
        key: type
      - action: delete
        key: mountpoint

  attributes/host_metrics_node:
    actions:
      - key: node.id
        value: $${NODE_ID}
        action: insert

  filter/drop_unsupported_types:
    metric_conditions:
      - metric.type == METRIC_DATA_TYPE_HISTOGRAM
      - metric.type == METRIC_DATA_TYPE_EXPONENTIAL_HISTOGRAM
      - metric.type == METRIC_DATA_TYPE_SUMMARY

  filter/otlp:
    metrics:
      include:
        match_type: regexp
        metric_names:
          - "orchestrator.*"
          - "template.*"
          - "api.*"
          - "db.sql.connection.*"
          - "vault.*"
          - "client_proxy.*"
          - "Click*"
          - "otelcol.*"
          - "pgxpool.*"
          - "e2b.*"

  filter/prometheus:
    metrics:
      include:
        match_type: strict
        metric_names:
          - "nomad_client.host_cpu_total_percent"
          - "nomad_client_host_cpu_idle"
          - "nomad_client_host_disk_available"
          - "nomad_client_host_disk_size"
          - "nomad_client_host_memory_available"
          - "nomad_client_host_memory_total"
          - "nomad_client_allocs_memory_usage"
          - "nomad_client_allocs_memory_allocated"
          - "nomad_client_allocs_cpu_total_ticks"
          - "nomad_client_allocs_cpu_allocated"

  metricstransform:
    transforms:
      - include: "nomad_client_host_cpu_idle"
        match_type: strict
        action: update
        operations:
          - action: aggregate_labels
            aggregation_type: sum
            label_set: [instance, node_id, node_status, node_pool]

  resourcedetection:
    detectors: [system]
    override: true
    system:
      hostname_sources: [os]

  transform/set-name:
    metric_statements:
      - delete_key(datapoint.attributes, "instance")
      - delete_key(datapoint.attributes, "node_id")
      - delete_key(datapoint.attributes, "node_scheduling_eligibility")
      - delete_key(datapoint.attributes, "node_class")
      - delete_key(datapoint.attributes, "node_status")
      - delete_key(datapoint.attributes, "service_name")
      - set(datapoint.attributes["service.instance.id"], resource.attributes["host.name"])

  filter/rpc_duration_only:
    metrics:
      include:
        match_type: regexp
        metric_names:
          - "rpc.server.duration.*"
  resource/remove_instance:
    attributes:
      - action: delete
        key: service.instance.id

extensions:
  health_check:
    endpoint: 0.0.0.0:13133

exporters:
  debug:
    verbosity: basic
  clickhouse:
    endpoint: "http://192.168.162.30:8123"
    database: "dev_e2b_clickhouse"
    username: "default"
    password: ""
    tls:
      insecure: true
    timeout: 10s
    create_schema: false
    metrics_tables:
      gauge:
        name: "metrics_gauge"
      sum:
        name: "metrics_sum"
    sending_queue:
      enabled: true
      num_consumers: 8      # 并发写入线程数，2万 sbx 场景提高到 8
      queue_size: 1000      # 最多积压 1000 批次，超出丢弃，防止内存无限增长
    retry_on_failure:
      enabled: true
      initial_interval: 5s
      max_interval: 60s
      max_elapsed_time: 300s  # 5 分钟内重试失败则放弃这批数据
  otlphttp/loki:
    endpoint: "http://192.168.162.30:3100/otlp"
    tls:
      insecure: true
    sending_queue:
      enabled: true
      num_consumers: 4
      queue_size: 500
    retry_on_failure:
      enabled: true
      initial_interval: 5s
      max_interval: 30s
      max_elapsed_time: 120s

service:
  telemetry:
    logs:
      level: info
    metrics:
      readers:
        - pull:
            exporter:
              prometheus:
                host: 0.0.0.0
                port: 8888
        - periodic:
            exporter:
              otlp:
                protocol: grpc
                insecure: true
                endpoint: localhost:4317
  extensions:
    - health_check
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, filter/otlp, filter/drop_unsupported_types, resourcedetection, transform/set-name, batch/clickhouse]
      exporters: [clickhouse]
    metrics/prometheus:
      receivers: [prometheus]
      processors: [memory_limiter, filter/prometheus, filter/drop_unsupported_types, metricstransform, resourcedetection, transform/set-name, batch/clickhouse]
      exporters: [clickhouse]
    metrics/rpc_only:
      receivers: [otlp]
      processors: [memory_limiter, filter/rpc_duration_only, filter/drop_unsupported_types, resource/remove_instance, resourcedetection, transform/set-name, batch/clickhouse]
      exporters: [clickhouse]
    metrics/host:
      receivers: [hostmetrics]
      processors: [memory_limiter, filter/drop_by_device, filter/drop_unsupported_types, attributes/strip_fs_labels, attributes/host_metrics_node, batch/clickhouse]
      exporters: [clickhouse]
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [debug]
    logs:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlphttp/loki]
EOF
        destination = "local/config/otel-collector-config.yaml"
      }
    }
  }
}