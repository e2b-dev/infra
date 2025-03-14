job "orchestrator" {
  type = "system"
  datacenters = ["${gcp_zone}"]

  priority = 90

  group "client-orchestrator" {
    network {
      port "orchestrator" {
        static = "${port}"
      }
    }

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

    task "start" {
      driver = "raw_exec"

      env {
        NODE_ID                      = "$${node.unique.id}"
        CONSUL_TOKEN                 = "${consul_acl_token}"
        OTEL_TRACING_PRINT           = "${otel_tracing_print}"
        LOGS_COLLECTOR_ADDRESS       = "${logs_collector_address}"
        LOGS_COLLECTOR_PUBLIC_IP     = "${logs_collector_public_ip}"
        ENVIRONMENT                  = "${environment}"
        TEMPLATE_BUCKET_NAME         = "${template_bucket_name}"
        OTEL_COLLECTOR_GRPC_ENDPOINT = "${otel_collector_grpc_endpoint}"
        CLICKHOUSE_CONNECTION_STRING = "${clickhouse_connection_string}"
        CLICKHOUSE_USERNAME          = "${clickhouse_username}"
        CLICKHOUSE_PASSWORD          = "${clickhouse_password}"
        CLICKHOUSE_DATABASE          = "${clickhouse_database}"
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " chmod +x local/orchestrator && local/orchestrator --port ${port} --proxy-port ${proxy_port}"]
      }

      artifact {
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/orchestrator"

        %{ if environment == "dev" }
        // Checksum in only available for dev to increase development speed in prod use rolling updates
        options {
          checksum = "md5:${orchestrator_checksum}"
        }
        %{ endif }
      }
    }
  }
}
