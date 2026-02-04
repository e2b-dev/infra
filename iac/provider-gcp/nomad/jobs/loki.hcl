job "loki" {
  datacenters = ["${gcp_zone}"]
  type        = "service"
  node_pool = "${node_pool}"

  priority = 75

  group "loki-service" {
    // Try to restart the task indefinitely
    // Tries to restart every 5 seconds
    restart {
      interval         = "5s"
      attempts         = 1
      delay            = "5s"
      mode             = "delay"
    }

    network {
      port "loki" {
        to = "${loki_service_port_number}"
      }

      %{ if prevent_colocation }
      port "scheduling-block" {
        // This port is used to block scheduling of jobs with the same block on the same node.
        // We use this to block API and Loki from being scheduled on the same node.
        static = 40234
      }
      %{ endif }
    }

    service {
      name = "loki"
      port = "${loki_service_port_name}"

      check {
        type     = "http"
        path     = "/ready"
        interval = "20s"
        timeout  = "2s"
        port     = "${loki_service_port_number}"
      }
    }

    task "loki" {
      driver = "docker"

      config {
        network_mode = "host"
        image = "grafana/loki:2.9.17"

        args = [
          "-config.file",
          "local/loki-config.yml",
        ]
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
  grpc_server_max_recv_msg_size: 104857600  # 100 Mb
  grpc_server_max_send_msg_size: 104857600  # 100 Mb

common:
  path_prefix: /loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory

storage_config:
  gcs:
    bucket_name: "${loki_bucket_name}"
    chunk_buffer_size: 2097152  # 2MB
  tsdb_shipper:
    active_index_directory: /loki/tsdb-shipper-active
    cache_location: /loki/tsdb-shipper-cache
    cache_ttl: 1h
    shared_store: gcs

chunk_store_config:
  chunk_cache_config:
    embedded_cache:
      enabled: true
      max_size_mb: 2048
      ttl: 30m

query_range:
  align_queries_with_step: true
  cache_results: true
  max_retries: 2
  results_cache:
    cache:
      embedded_cache:
        enabled: true
        max_size_mb: 2048
        ttl: 30m

ingester_client:
  grpc_client_config:
    max_recv_msg_size: 104857600  # 100 Mb
    max_send_msg_size: 104857600  # 100 Mb

ingester:
  chunk_idle_period: 10m
  chunk_encoding: snappy
  max_chunk_age: 15m
  chunk_target_size: 1048576  # 1MB
  wal:
    dir: /loki/wal
    enabled: false
    flush_on_shutdown: true

schema_config:
 configs:
    - from: 2024-03-05
      store: tsdb
      object_store: gcs
      schema: v12
      index:
        prefix: loki_index_
        period: 24h

compactor:
  working_directory: /loki/compactor
  compaction_interval: 10m
  retention_enabled: true
  retention_delete_delay: 2h
  retention_delete_worker_count: 150
  shared_store: gcs

# The bucket lifecycle policy should be set to delete objects after MORE than the specified retention period
limits_config:
  retention_period: 168h
  ingestion_rate_mb: 100
  ingestion_burst_size_mb: 500
  per_stream_rate_limit: "80MB"
  per_stream_rate_limit_burst: "240MB"
  max_streams_per_user: 0
  split_queries_by_interval: 30m
  max_global_streams_per_user: 0
  unordered_writes: true
  reject_old_samples_max_age: 168h
EOF

        destination = "local/loki-config.yml"
      }
    }
  }
}
