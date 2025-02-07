job "client-proxy" {
  datacenters = ["${gcp_zone}"]
  node_pool = "api"

  priority = 80

  group "client-proxy" {
    network {
      port "${port_name}" {
        static = "${port_number}"
      }

      port "health" {
        static = "${health_port_number}"
      }
    }

    service {
      name = "proxy"
      port = "${port_name}"


      check {
        type     = "http"
        name     = "health"
        path     = "/"
        interval = "20s"
        timeout  = "5s"
        port     = "health"
      }
    }

    task "start" {
      driver = "docker"

      resources {
        memory_max = 4096
        memory     = 1024
        cpu        = 1000
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
