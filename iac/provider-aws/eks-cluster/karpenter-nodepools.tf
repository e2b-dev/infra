# --- EC2NodeClass for Firecracker-capable nodes ---
resource "kubectl_manifest" "ec2nodeclass_c8i_firecracker" {
  yaml_body = yamlencode({
    apiVersion = "karpenter.k8s.aws/v1"
    kind       = "EC2NodeClass"
    metadata = {
      name = "c8i-firecracker"
    }
    spec = {
      role = module.karpenter.node_iam_role_name

      amiSelectorTerms = [
        {
          id = var.eks_ami_id
        }
      ]

      subnetSelectorTerms = [
        {
          tags = {
            "karpenter.sh/discovery" = var.cluster_name
          }
        }
      ]

      securityGroupSelectorTerms = [
        {
          tags = {
            "karpenter.sh/discovery" = var.cluster_name
          }
        }
      ]

      blockDeviceMappings = [
        {
          deviceName = "/dev/xvda"
          ebs = {
            volumeSize          = "${var.boot_disk_size_gb}Gi"
            volumeType          = "gp3"
            deleteOnTermination = true
            encrypted           = true
          }
        },
        {
          deviceName = "/dev/xvdb"
          ebs = {
            volumeSize          = "${var.cache_disk_size_gb}Gi"
            volumeType          = "gp3"
            iops                = var.cache_disk_iops
            throughput          = var.cache_disk_throughput_mbps
            deleteOnTermination = true
            encrypted           = true
          }
        }
      ]

      userData = templatefile("${path.module}/templates/node-userdata.sh", {
        EFS_DNS_NAME         = var.efs_dns_name
        EFS_MOUNT_PATH       = var.efs_mount_path
        CACHE_DISK_DEVICE    = "/dev/xvdb"
        CACHE_MOUNT_PATH     = "/mnt/cache"
        HUGEPAGES_PERCENTAGE = var.client_hugepages_percentage
      })

      tags = merge(var.tags, {
        "karpenter.sh/discovery" = var.cluster_name
      })
    }
  })

  depends_on = [helm_release.karpenter]
}

# --- Client NodePool (orchestrator workloads) ---
resource "kubectl_manifest" "nodepool_client" {
  yaml_body = yamlencode({
    apiVersion = "karpenter.sh/v1"
    kind       = "NodePool"
    metadata = {
      name = "client"
    }
    spec = {
      template = {
        metadata = {
          labels = {
            "e2b.dev/node-pool" = "client"
          }
        }
        spec = {
          nodeClassRef = {
            group = "karpenter.k8s.aws"
            kind  = "EC2NodeClass"
            name  = "c8i-firecracker"
          }
          requirements = [
            {
              key      = "kubernetes.io/arch"
              operator = "In"
              values   = ["amd64"]
            },
            {
              key      = "karpenter.sh/capacity-type"
              operator = "In"
              values   = var.client_capacity_types
            },
            {
              key      = "node.kubernetes.io/instance-type"
              operator = "In"
              values   = var.client_instance_types
            }
          ]
          taints = [
            {
              key    = "e2b.dev/node-pool"
              value  = "client"
              effect = "NoSchedule"
            }
          ]
          topologySpreadConstraints = [
            {
              maxSkew           = 1
              topologyKey       = "topology.kubernetes.io/zone"
              whenUnsatisfiable = "ScheduleAnyway"
            }
          ]
        }
      }
      disruption = {
        consolidationPolicy = "WhenEmptyOrUnderutilized"
        consolidateAfter    = var.client_consolidation_after
      }
      limits = {
        cpu    = "1000"
        memory = "2000Gi"
      }
    }
  })

  depends_on = [kubectl_manifest.ec2nodeclass_c8i_firecracker]
}

# --- Build NodePool (template-manager workloads, scale-to-zero) ---
resource "kubectl_manifest" "nodepool_build" {
  yaml_body = yamlencode({
    apiVersion = "karpenter.sh/v1"
    kind       = "NodePool"
    metadata = {
      name = "build"
    }
    spec = {
      template = {
        metadata = {
          labels = {
            "e2b.dev/node-pool" = "build"
          }
        }
        spec = {
          nodeClassRef = {
            group = "karpenter.k8s.aws"
            kind  = "EC2NodeClass"
            name  = "c8i-firecracker"
          }
          requirements = [
            {
              key      = "kubernetes.io/arch"
              operator = "In"
              values   = ["amd64"]
            },
            {
              key      = "karpenter.sh/capacity-type"
              operator = "In"
              values   = ["spot", "on-demand"]
            },
            {
              key      = "node.kubernetes.io/instance-type"
              operator = "In"
              values   = var.build_instance_types
            }
          ]
          taints = [
            {
              key    = "e2b.dev/node-pool"
              value  = "build"
              effect = "NoSchedule"
            }
          ]
          topologySpreadConstraints = [
            {
              maxSkew           = 1
              topologyKey       = "topology.kubernetes.io/zone"
              whenUnsatisfiable = "ScheduleAnyway"
            }
          ]
        }
      }
      disruption = {
        consolidationPolicy = "WhenEmptyOrUnderutilized"
        consolidateAfter    = var.build_consolidation_after
      }
      limits = {
        cpu    = "500"
        memory = "1000Gi"
      }
    }
  })

  depends_on = [kubectl_manifest.ec2nodeclass_c8i_firecracker]
}
