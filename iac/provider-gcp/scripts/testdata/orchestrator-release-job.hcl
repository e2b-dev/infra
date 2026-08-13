job "orchestrator-dev" {
  type      = "system"
  node_pool = "default"

  group "client-orchestrator" {
    network {
      port "orchestrator" {
        static = 5008
      }

      port "orchestrator-proxy" {
        static = 5007
      }
    }

    task "start" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["-c", " chmod +x local/orchestrator && local/orchestrator"]
      }

      artifact {
        source      = "gcs::https://www.googleapis.com/storage/v1/monad-code-fc-env-pipeline/orchestrator.0123456789ab#2001"
        destination = "local/orchestrator"
        mode        = "file"
      }
    }
  }
}
