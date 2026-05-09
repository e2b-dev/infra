# Hetzner Network Module
# Analog provider-aws/modules/network/

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.45"
    }
  }
}

# Cloud Network (10.0.0.0/8)
resource "hcloud_network" "maxicore_prod" {
  name     = var.network_name
  ip_range = var.ip_range
  labels = {
    project = "maxicore"
    env     = var.env
  }
}

# Cloud Subnet (10.0.1.0/24)
resource "hcloud_network_subnet" "cloud" {
  network_id   = hcloud_network.maxicore_prod.id
  type         = "cloud"
  network_zone = var.network_zone
  ip_range     = var.cloud_ip_range
}

# vSwitch Subnet (10.10.0.0/24)
# NOTE: vSwitch must be pre-created via Hetzner Robot Webservice
# This binds the existing vswitch to the cloud network
resource "hcloud_network_subnet" "vswitch" {
  count        = var.vswitch_id != null ? 1 : 0
  network_id   = hcloud_network.maxicore_prod.id
  type         = "vswitch"
  network_zone = var.network_zone
  ip_range     = var.vswitch_ip_range
  vswitch_id   = var.vswitch_id
}
