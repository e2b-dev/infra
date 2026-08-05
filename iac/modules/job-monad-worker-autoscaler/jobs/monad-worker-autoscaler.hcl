job "monad-worker-autoscaler-shadow" {
  type      = "service"
  node_pool = "${node_pool}"
  priority  = 90

  group "observer" {
    count = ${allocation_count}

    constraint {
      operator = "distinct_hosts"
      value    = "true"
    }

    update {
      max_parallel      = 1
      min_healthy_time  = "10s"
      healthy_deadline  = "2m"
      progress_deadline = "3m"
      auto_revert       = true
    }

    restart {
      attempts = 3
      interval = "5m"
      delay    = "10s"
      mode     = "delay"
    }

    network {
      mode = "host"
      port "metrics" {
        static = ${metrics_port}
      }
    }

    service {
      name     = "monad-worker-autoscaler-shadow"
      port     = "metrics"
      provider = "nomad"

      check {
        type     = "http"
        path     = "/healthz"
        interval = "10s"
        timeout  = "2s"
      }
    }

    task "observe" {
      driver = "raw_exec"

      env {
        MONAD_WORKER_AUTOSCALER_MODE             = "shadow"
        MONAD_WORKER_AUTOSCALER_MUTATION_ENABLED = "false"
        TAMS_OPS_CAPACITY_URL                    = "${tams_capacity_url}"
        TAMS_OPS_AUDIENCE                        = "${tams_audience}"
        NOMAD_ADDR                               = "http://127.0.0.1:4646"
        NOMAD_TOKEN                              = "${nomad_token}"
        NOMAD_NODE_POOL                          = "${worker_node_pool}"
        CONSUL_HTTP_ADDR                         = "http://127.0.0.1:8500"
        CONSUL_HTTP_TOKEN                        = "${consul_token}"
        CONSUL_LOCK_KEY                          = "service/monad-worker-autoscaler/leader"
        CONTROLLER_INSTANCE_ID                   = "$${NOMAD_ALLOC_ID}"
        METRICS_ADDR                             = "0.0.0.0:${metrics_port}"
        RECONCILE_INTERVAL                       = "10s"
      }

      config {
        command = "/bin/bash"
        args    = ["-ceu", "chmod +x local/monad-worker-autoscaler && exec local/monad-worker-autoscaler"]
      }

      artifact {
        source      = "${artifact_source}"
        destination = "local/monad-worker-autoscaler"
        mode        = "file"
      }

      resources {
        cpu    = 100
        memory = 128
      }
    }
  }
}
