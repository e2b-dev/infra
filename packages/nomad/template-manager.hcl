job "template-manager" {
  datacenters = ["${gcp_zone}"]
  node_pool  = "build"
  priority = 70

%{ if update_stanza }
  update {
      auto_promote      = true # Whether to promote the canary if the rest of the group is not healthy
      canary            = 1 # Allows to spawn new version of the service before killing the old one
      progress_deadline = "20m" # Deadline for the update to be completed
  }
%{ endif }

  group "template-manager" {
    network {
      port "template-manager" {
        static = "${port}"
      }
    }

    service {
      name = "template-manager"
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

    task "start" {
      driver = "raw_exec"

%{ if update_stanza }
      # https://developer.hashicorp.com/nomad/docs/configuration/client#max_kill_timeout
      kill_timeout      = "20m"
%{ endif }
      kill_signal  = "SIGTERM"

      resources {
        memory     = 1024
        cpu        = 256
      }

      env {
        GOOGLE_SERVICE_ACCOUNT_BASE64 = "${google_service_account_key}"
        GCP_PROJECT_ID                = "${gcp_project}"
        GCP_REGION                    = "${gcp_region}"
        GCP_DOCKER_REPOSITORY_NAME    = "${docker_registry}"
        API_SECRET                    = "${api_secret}"
        OTEL_TRACING_PRINT            = "${otel_tracing_print}"
        ENVIRONMENT                   = "${environment}"
        TEMPLATE_BUCKET_NAME          = "${template_bucket_name}"
        OTEL_COLLECTOR_GRPC_ENDPOINT  = "${otel_collector_grpc_endpoint}"
        LOGS_COLLECTOR_ADDRESS        = "${logs_collector_address}"
      }

      config {
        command = "/bin/bash"
        args    = ["-c", " chmod +x local/template-manager && local/template-manager --port ${port}"]
      }

      artifact {
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/template-manager"
        options {
            checksum    = "md5:${template_manager_checksum}"
        }
      }
    }
  }
}
