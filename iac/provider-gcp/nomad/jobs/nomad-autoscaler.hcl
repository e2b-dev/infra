job "nomad-autoscaler" {
  type     = "service"
  node_pool = "${node_pool}"

  group "autoscaler" {
    count = 1

    network {
      port "http" {}
    }

    task "autoscaler" {
      driver = "raw_exec"

      config {
        command = "/bin/bash"
        args    = ["-c", "chmod +x local/plugins/nomad-nodepool-apm && ./local/nomad-autoscaler agent -config local/config.hcl -plugin-dir local/plugins"]
      }

      artifact {
        source      = "https://releases.hashicorp.com/nomad-autoscaler/${autoscaler_version}/nomad-autoscaler_${autoscaler_version}_linux_amd64.zip"
        destination = "local"
      }

      # Custom nodepool APM plugin
      artifact {
        source      = "gcs::https://www.googleapis.com/storage/v1/${bucket_name}/nomad-nodepool-apm"
        destination = "local/plugins/nomad-nodepool-apm"
        mode        = "file"
        options {
          checksum = "md5:${nomad_nodepool_apm_checksum}"
        }
      }

      template {
        data        = <<-EOF
          # Nomad Autoscaler configuration
          
          nomad {
            address = "http://{{ env "NOMAD_IP_http" }}:4646"
            token   = "${nomad_token}"
          }

          # Enable the HTTP health API
          http {
            bind_address = "0.0.0.0"
            bind_port    = {{ env "NOMAD_PORT_http" }}
          }

          # Policy configuration
          # Policies are defined in Nomad job scaling blocks, not files
          policy {
            default_cooldown = "2m"
          }

          # Plugin directory for external plugins
          plugin_dir = "local/plugins"

          # APM plugins configuration - custom plugin for node pool count
          apm "nomad-nodepool-apm" {
            driver = "nomad-nodepool-apm"
            config = {
              nomad_address = "http://{{ env "NOMAD_IP_http" }}:4646"
              nomad_token   = "${nomad_token}"
            }
          }

          # Use built-in nomad-target for job scaling (no config needed, uses nomad block above)
        EOF
        destination = "local/config.hcl"
      }

      resources {
        cpu    = 256
        memory = 256
      }

      service {
        name     = "nomad-autoscaler"
        port     = "http"
        provider = "nomad"

        check {
          type     = "http"
          path     = "/v1/health"
          interval = "10s"
          timeout  = "2s"
        }
      }
    }
  }
}

