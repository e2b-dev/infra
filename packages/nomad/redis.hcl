variable "gcp_zone" {
  type    = string
  default = "us-central1-a"
}

variable "image_name" {
  type    = string
  default = "redis:7.4.2-alpine"
}

variable "redis_port_number" {
  type    = number
  default = 6379
}

variable "redis_port_name" {
  type    = string
  default = "redis"
}

job "redis" {
  datacenters = [var.gcp_zone]
  node_pool = "api"
  priority = 95

  group "api-service" {
    network {
      port "redis" {
        static = var.redis_port_number
      }
    }

    service {
      name = "redis"
      port = var.redis_port_name

      check {
        type     = "tcp"
        name     = "health"
        interval = "10s"
        timeout  = "2s"
        port     = var.redis_port_number
      }
    }

    task "start" {
      driver = "docker"

      resources {
        memory_max = 4096
        memory     = 2048
        cpu        = 1024
      }

      config {
        network_mode = "host"
        image        = var.image_name
        ports        = [var.redis_port_name]
        args = [
        ]
      }
    }
  }
}
