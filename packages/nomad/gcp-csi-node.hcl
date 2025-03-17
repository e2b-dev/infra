job "gcp-csi-node" {
  datacenters = ["${zone}"]
  type        = "system"    // System jobs run on all nodes
  node_pool   = "api"

  group "node" {
    task "plugin" {
      driver = "docker"

      config {
        image = "gcr.io/gke-release/gcp-compute-persistent-disk-csi-driver:v1.9.7-gke.2"
        
        // Node plugins need privileged access to mount volumes
        privileged = true
        
        args = [
          "--endpoint=unix://csi/csi.sock",
          "--v=5",
        ]

        // Mount required host paths for node operations
        mount {
          type     = "bind"
          source   = "/dev"
          target   = "/dev"
        }
        mount {
          type     = "bind"
          source   = "/sys"
          target   = "/sys"
        }
        mount {
          type     = "bind"
          source   = "/var/lib/kubelet"
          target   = "/var/lib/kubelet"
          readonly = false
        }
      }

      csi_plugin {
        id        = "gcp-pd"
        type      = "node"    // Specifies this as a node plugin
        mount_dir = "/csi"
      }

      resources {
        cpu    = 500
        memory = 256
      }
    }
  }
} 