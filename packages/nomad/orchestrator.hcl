job "orchestrator" {
  type = "system"
  datacenters = ["${gcp_zone}"]
  node_pool = "default"

  priority = 90

  meta {
    // In "dev" environment this will force a restart of orchestrator job when you upload new one.
    restart_checksum = %{ if environment == "dev" }"${orchestrator_checksum}"%{ else }""%{ endif }
  }

  group "orchestrator" {
    network {
      port "orchestrator" {
        static = "${port}"
      }

      port "proxy" {
        static = "${proxy_port}"
      }
    }

    service {
      name = "orchestrator"
      port = "orchestrator"

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
      port = "proxy"
    }

    task "start" {
      driver = "raw_exec"
      
      restart {
        attempts = 0
      }

      env {
        // We need to pass this env via HCL jobspec as it is not available during terraform templating,
        // but filled via Consul when the allocation is created.
        NODE_ID = "$${node.unique.id}"
      }

      template {
        destination = "local/start.sh"
        // Make this "noop" for "prod" and "restart" for "dev" to increase development speed.
        // As we will move to orchestrator rolling updates we can try the "signal" or "script" change mode.
        change_mode = %{ if environment == "dev" }"restart"%{ else }"noop"%{ endif }
        // We template the whole script this way, because otherwise, although changes to the env vars do not force whole job restart,
        // changes to the actual content of the inlined script would.
        data = <<EOT
{{ with nomadVar "nomad/jobs/orchestrator" }}{{ .start_script }}{{ end }}
EOT
      }

      config {
        command = "local/start.sh"
      }
    }
  }
}
