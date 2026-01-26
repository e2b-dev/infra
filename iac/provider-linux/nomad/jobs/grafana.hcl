job "grafana" {
  datacenters = ["${datacenter}"]
  node_pool   = "${node_pool}"
  type        = "service"

  priority = 75

  group "grafana-service" {
    restart {
      interval = "5s"
      attempts = 1
      delay    = "5s"
      mode     = "delay"
    }

    network {
      port "${grafana_service_port_name}" { to = "${grafana_service_port_number}" }
    }

    service {
      name = "grafana"
      port = "${grafana_service_port_name}"

      check {
        type     = "http"
        path     = "/api/health"
        interval = "20s"
        timeout  = "2s"
        port     = "${grafana_service_port_name}"
      }
    }

    task "grafana" {
      driver = "docker"

      config {
        network_mode = "host"
%{ if docker_image_prefix != "" }
        image = "${docker_image_prefix}/grafana/grafana:10.4.3"
%{ else }
        image = "grafana/grafana:10.4.3"
%{ endif }
      }

      resources {
        memory_max = ${memory_mb * 1.5}
        memory     = ${memory_mb}
        cpu        = ${cpu_count * 1000}
      }

      env {
        GF_SERVER_HTTP_PORT = "${grafana_service_port_number}"
        GF_LOG_LEVEL        = "warn"
        GF_SECURITY_ADMIN_USER     = "admin"
        GF_SECURITY_ADMIN_PASSWORD = "admin"
        GF_PATHS_PROVISIONING      = "/local/provisioning"
      }

      template {
        destination   = "local/provisioning/datasources/datasource.yml"
        change_mode   = "restart"
        data            = <<EOH
apiVersion: 1
deleteDatasources:
  - name: Loki
    uid: loki
datasources:
  - name: Loki
    uid: loki
    type: loki
    access: proxy
    orgId: 1
    url: "http://loki.service.consul:${loki_service_port_number}"
    isDefault: true
    version: 1
    editable: false
    jsonData:
      httpMethod: GET
      maxLines: 1000
EOH
      }
    }
  }
}
