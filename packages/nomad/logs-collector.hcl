job "logs-collector" {
  datacenters = ["${gcp_zone}"]
  type        = "system"
  node_pool    = "all"

  priority = 85

  group "logs-collector" {
    network {
      port "health" {
        to = "${logs_health_port_number}"
      }
      port "logs" {
        to = "${logs_port_number}"
      }
    }

    service {
      name = "logs-collector"
      port = "logs"
      tags = [
        "logs",
        "health",
      ]

      check {
        type     = "http"
        name     = "health"
        path     = "${logs_health_path}"
        interval = "20s"
        timeout  = "5s"
        port     = "${logs_health_port_number}"
      }
    }

    task "start-collector" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "timberio/vector:0.34.X-alpine"

        ports = [
          "health",
          "logs",
        ]
      }

      env {
        VECTOR_CONFIG          = "local/vector.toml"
        VECTOR_REQUIRE_HEALTHY = "true"
        VECTOR_LOG             = "warn"
      }

      resources {
        memory_max = 4096
        memory     = 512
        cpu        = 500
      }

      template {
        destination   = "local/vector.toml"
        change_mode   = "signal"
        change_signal = "SIGHUP"
        # overriding the delimiters to [[ ]] to avoid conflicts with Vector's native templating, which also uses {{ }}
        left_delimiter  = "[["
        right_delimiter = "]]"
        data            = <<EOH
data_dir = "alloc/data/vector/"

[api]
enabled = true
address = "0.0.0.0:${logs_health_port_number}"

[sources.http_server]
type = "http_server"
address = "0.0.0.0:${logs_port_number}"
encoding = "ndjson"
path_key = "_path"

[transforms.add_source_http_server]
type = "remap"
inputs = ["http_server"]
source = """
del(."_path")
.sandboxID = .instanceID
.timestamp = parse_timestamp(.timestamp, format: "%Y-%m-%dT%H:%M:%S.%fZ") ?? now()
if !exists(.envID) {
  .envID = "unknown"
}
if !exists(.category) {
  .category = "default"
}
if !exists(.teamID) {
  .teamID = "unknown"
}
if !exists(.sandboxID) {
  .sandboxID = "unknown"
}
if !exists(.buildID) {
  .buildID = "unknown"
}
if !exists(.service) {
  .service = "envd"
 }
"""

[transforms.internal_routing]
type = "route"
inputs = [ "add_source_http_server" ]

[transforms.internal_routing.route]
internal = '.internal == true'

[transforms.remove_internal]
type = "remap"
inputs = [ "internal_routing._unmatched" ]
source = '''
del(.internal)
'''

[sinks.local_loki_logs]
type = "loki"
inputs = [ "remove_internal" ]
endpoint = "http://loki.service.consul:${loki_service_port_number}"
encoding.codec = "json"

[sinks.local_loki_logs.labels]
source = "logs-collector"
service = "{{ service }}"
teamID = "{{ teamID }}"
envID = "{{ envID }}"
buildID = "{{ buildID }}"
sandboxID = "{{ sandboxID }}"
category = "{{ category }}"

%{ if grafana_logs_endpoint != " " }
[sinks.grafana]
type = "loki"
inputs = [ "internal_routing.internal" ]
endpoint = "${grafana_logs_endpoint}"
encoding.codec = "json"
auth.strategy = "basic"
auth.user = "${grafana_logs_user}"
auth.password = "${grafana_api_key}"

[sinks.grafana.labels]
source = "logs-collector"
service = "{{ service }}"
teamID = "{{ teamID }}"
envID = "{{ envID }}"
sandboxID = "{{ sandboxID }}"
%{ endif }
        EOH
      }
    }
  }
}
