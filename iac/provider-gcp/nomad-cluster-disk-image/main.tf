terraform {
  required_version = "=1.7.5"

  backend "gcs" {
    prefix = "terraform/cluster-disk-image/state"
  }

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "6.49.3"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
}

resource "google_compute_network" "packer_network" {
  project                                   = var.gcp_project_id
  name                                      = var.network_name
  auto_create_subnetworks                   = false
  delete_default_routes_on_create           = false
  enable_ula_internal_ipv6                  = false
  mtu                                       = 1460
  network_firewall_policy_enforcement_order = "AFTER_CLASSIC_FIREWALL"
  routing_mode                              = "REGIONAL"

  lifecycle {
    prevent_destroy = true
  }
}

resource "google_compute_subnetwork" "packer_subnetwork" {
  ip_cidr_range                    = "10.0.0.0/8"
  name                             = var.subnet_name
  project                          = var.gcp_project_id
  region                           = var.gcp_region
  network                          = google_compute_network.packer_network.id
  private_ip_google_access         = false
  send_secondary_ip_range_if_empty = false
  stack_type                       = "IPV4_ONLY"

  log_config {
    aggregation_interval = "INTERVAL_15_MIN"
    flow_sampling        = 0
    metadata             = "EXCLUDE_ALL_METADATA"
  }

  lifecycle {
    prevent_destroy = true
  }
}


resource "google_compute_firewall" "internal_remote_connection_firewall_ingress" {
  name               = "${var.network_name}-firewall-ingress"
  project            = var.gcp_project_id
  network            = google_compute_network.packer_network.name
  disabled           = false
  destination_ranges = []

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  priority = 900

  direction = "INGRESS"
  # https://googlecloudplatform.github.io/iap-desktop/setup-iap/
  source_ranges = ["35.235.240.0/20"]

  lifecycle {
    prevent_destroy = true
  }
}
