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
      }

      csi_plugin {
        id        = "gcp-pd"
        type      = "node"    // Specifies this as a node plugin
        mount_dir = "/csi"
        # https://discuss.hashicorp.com/t/csi-controller-fails-with-gprc-error/51920/2

      }

      resources {
        cpu    = 500
        memory = 256
      }
    }
  }
} 
