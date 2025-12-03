job "logs-collector" {
  type        = "system"
  node_pool   = "all"

  priority = 85

  group "logs-collector" {
    restart {
      interval = "5s"
      attempts = 1
      delay    = "5s"
      mode     = "delay"
    }

    network {
      port "health" { to = "${logs_health_port_number}" }
      port "logs"   { to = "${logs_port_number}" }
    }

    service {
      name = "logs-collector"
      port = "logs"
      tags = ["logs","health"]

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
%{ if docker_image_prefix != "" }
        image        = "${docker_image_prefix}/timberio/vector:0.34.X-alpine"
%{ else }
        image        = "timberio/vector:0.34.X-alpine"
%{ endif }
        ports        = ["health","logs"]
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
del("._path")
.sandboxID = .instanceID
.timestamp = parse_timestamp(.timestamp, format: "%+") ?? now()
if exists(.sandbox_id) { .sandboxID = .sandbox_id }
if exists(.build_id) { .buildID = .build_id }
if exists(.env_id) { .envID = .env_id }
if exists(.team_id) { .teamID = .team_id }
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
out_of_order_action = "accept"

[sinks.local_loki_logs.labels]
source = "logs-collector"
service = "{{ service }}"
teamID = "{{ teamID }}"
envID = "{{ envID }}"
buildID = "{{ buildID }}"
sandboxID = "{{ sandboxID }}"
category = "{{ category }}"
        EOH
      }
    }
  }
}