job "logs-collector" {
  type      = "system"
  node_pool = "all"

  priority = 85

  group "logs-collector" {
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
        to = "9096"
      }
      port "logs" {
        # Changed port from 9095 to 19095 to avoid conflict
        to = "19095"
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
        path     = "/health"
        interval = "20s"
        timeout  = "5s"
        port     = "9096"
      }
    }

    task "start-collector" {
      driver = "docker"

      config {
        network_mode = "host"
        image        = "bp-docker-io-cn-shanghai.cr.volces.com/timberio/vector:0.51.X-alpine"
        #image        = "timberio/vector:0.34.X-alpine"
        security_opt = ["seccomp=unconfined"]

        volumes = [
          "/data2/e2b-log:/data2/e2b-log"
        ]
      }

      env {
        VECTOR_CONFIG          = "local/vector.toml"
        VECTOR_REQUIRE_HEALTHY = "true"
        VECTOR_LOG             = "warn"
      }

      resources {
        memory_max = 4096
        memory     = 2048
        cpu        = 500
      }

      template {
        destination     = "local/vector.toml"
        change_mode     = "signal"
        change_signal   = "SIGHUP"
        left_delimiter  = "[["
        right_delimiter = "]]"
        data            = <<EOH
data_dir = "alloc/data/vector/"

[api]
enabled = true
address = "0.0.0.0:9096"

[sources.http_server]
type = "http_server"
# Changed port from 9095 to 19095 to avoid conflict
address = "0.0.0.0:19095"
encoding = "ndjson"
path_key = "_path"

[transforms.add_source_http_server]
type = "remap"
inputs = ["http_server"]
source = """
del(."_path")
.sandboxID = .instanceID
.timestamp = parse_timestamp(.timestamp, format: "%+") ?? now()

# Normalize keys
if exists(.sandbox_id) {
  .sandboxID = .sandbox_id
  del(.sandbox_id)
}
if exists(.build_id) {
  .buildID = .build_id
  del(.build_id)
}
if exists(.env_id) {
  .envID = .env_id
  del(.env_id)
}
if exists(.team_id) {
  .teamID = .team_id
  del(.team_id)
}
if exists(."template.id") {
  .templateID = ."template.id"
  del(."template.id")
}
if exists(."sandbox.id") {
  .sandboxID = ."sandbox.id"
  del(."sandbox.id")
}
if exists(."build.id") {
  .buildID = ."build.id"
  del(."build.id")
}
if exists(."env.id") {
  .envID = ."env.id"
  del(."env.id")
}
if exists(."team.id") {
  .teamID = ."team.id"
  del(."team.id")
}

# Apply defaults if not already set
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
endpoint = "http://192.168.162.212:3100"
encoding.codec = "json"
compression = "none"
out_of_order_action = "accept"
# Disable the healthcheck to allow Vector to start even if Loki is temporarily unavailable.
healthcheck.enabled = false

[sinks.local_loki_logs.labels]
source = "logs-collector"
service = "{{ service }}"
teamID = "{{ teamID }}"
envID = "{{ envID }}"
buildID = "{{ buildID }}"
sandboxID = "{{ sandboxID }}"
category = "{{ category }}"

# 日志写入宿主机目录，宿主机上的 logcollector systemd 服务会采集此文件
[sinks.volcengine_tls_file]
type = "file"
inputs = [ "remove_internal" ]
path = "/data2/e2b-log/e2b-logs.jsonl"
encoding.codec = "json"
compression = "none"

        EOH
      }
    }
  }
}