job "orchestrator-${latest_orchestrator_job_id}" {
  type = "system"
  node_pool = "default"

  priority = 90

  group "client-orchestrator" {
    service {
      name = "orchestrator"
      port = "${port}"

      check {
        type         = "grpc"
        name         = "health"
        interval     = "20s"
        timeout      = "5s"
        grpc_use_tls = false
        port         = "${port}"
      }
    }

    service {
      name = "orchestrator-proxy"
      port = "${proxy_port}"
    }

    task "check-placement" {
      driver = "raw_exec"

      lifecycle {
        hook = "prestart"
        sidecar = false
      }

      restart {
        attempts = 0
      }

      template {
        destination = "local/check-placement.sh"
        data = <<EOT
#!/bin/bash

if [ "{{with nomadVar "nomad/jobs" }}{{ .latest_orchestrator_job_id }}{{ end }}" != "${latest_orchestrator_job_id}" ]; then
  echo "This orchestrator is not the latest version, exiting"
  exit 1
fi
EOT
      }

      config {
        command = "local/check-placement.sh"
      }
    }

    task "start" {
      driver = "raw_exec"

      restart {
        attempts = 0
      }

      env {
        NODE_ID                      = "$${node.unique.name}"
        CONSUL_TOKEN                 = "${consul_acl_token}"
        OTEL_TRACING_PRINT           = "${otel_tracing_print}"
        LOGS_COLLECTOR_ADDRESS       = "${logs_collector_address}"
        LOGS_COLLECTOR_PUBLIC_IP     = "${logs_collector_public_ip}"
        ENVIRONMENT                  = "${environment}"
        TEMPLATE_BUCKET_NAME         = "${template_bucket_name}"
        TEMPLATE_CACHE_PROXY_URL     = "${template_cache_proxy_url}"
        OTEL_COLLECTOR_GRPC_ENDPOINT = "${otel_collector_grpc_endpoint}"
        CLICKHOUSE_CONNECTION_STRING = "${clickhouse_connection_string}"
        CLICKHOUSE_USERNAME          = "${clickhouse_username}"
        CLICKHOUSE_PASSWORD          = "${clickhouse_password}"
        CLICKHOUSE_DATABASE          = "${clickhouse_database}"
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " chmod +x local/orchestrator && local/orchestrator --port ${port} --proxy-port ${proxy_port} --wait 0"]
      }

      artifact {
        %{ if environment == "dev" }
        // Version hash is only available for dev to increase development speed in prod use rolling updates
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/orchestrator?version=${orchestrator_checksum}"
        %{ else }
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/orchestrator"
        %{ endif }
      }
    }
  }
}
