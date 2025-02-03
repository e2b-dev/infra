job "proxy" {
  datacenters = ["${gcp_zone}"]
  node_pool = "api"

  priority = 70

  group "proxy" {
    network {
      port "${port_name}" {
        static = "${port_number}"
      }
    }

    service {
      name = "proxy"
      port = "${port_name}"
    }

    task "start" {
      driver = "docker"

      resources {
        memory     = 1024
        cpu        = 256
      }


      config {
        network_mode = "host"
        image        = "${image_name}"
        ports        = ["${port_name}"]
        args         = ["--port", "${port_number}"]
      }
    }
  }
}
