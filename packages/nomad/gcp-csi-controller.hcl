job "gcp-csi-controller" {
  datacenters = ["${zone}"]
  type        = "service"
  node_pool   = "api"

  group "controller" {
    task "plugin" {
      driver = "docker"

      config {
        image = "gcr.io/gke-release/gcp-compute-persistent-disk-csi-driver:v1.9.7-gke.2"
        args = [
          "--endpoint=unix://csi/csi.sock",
          "--v=5",
        ]
      }

      csi_plugin {
        id        = "gcp-pd"
        type      = "controller"
        mount_dir = "/csi"
      }

      resources {
        cpu    = 500
        memory = 256
      }
    }
  }
} 
