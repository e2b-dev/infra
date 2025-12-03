job "network-policy-init" {
  datacenters = ["${datacenter}"]
  node_pool   = "${node_pool}"
  type        = "system"
  priority    = 60

  group "init" {
    restart {
      attempts = 0
    }

    task "apply" {
      driver = "raw_exec"

      env {
        OPEN_PORTS = "${ports}"
      }

      template {
        data        = <<EOH
${start_script}
EOH
        destination = "local/start.sh"
        perms       = "0755"
      }

      config {
        command = "local/start.sh"
      }
    }
  }
}
