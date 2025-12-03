job "nfs-server" {
  datacenters = ["${datacenter}"]
  node_pool   = "${node_pool}"
  type        = "system"
  priority    = 50

  constraint {
    attribute = "$${attr.unique.network.ip-address}"
    operator  = "="
    value     = "${server_ip}"
  }

  group "nfs" {
    restart {
      attempts = 0
    }

    network {
      port "nfs" {
        static = 2049
      }
    }

    service {
      name         = "nfs-server"
      provider     = "nomad"
      address_mode = "host"
      port         = "nfs"
      check {
        type     = "tcp"
        name     = "nfs-healthy"
        interval = "10s"
        timeout  = "2s"
      }
    }

    task "start" {
      driver = "raw_exec"

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
