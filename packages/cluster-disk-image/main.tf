terraform {
  required_version = ">= 1.5.0, < 1.6.0"
  backend "gcs" {
    prefix = "terraform/cluster-disk-image/state"
  }
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "5.31.0"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
}

resource "google_compute_network" "packer_network" {
  name                    = var.network_name
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "packer_subnetwork" {
  ip_cidr_range = "10.0.0.0/8"
  name          = "${var.network_name}-subnetwork"
  network       = google_compute_network.packer_network.id
}


resource "google_compute_firewall" "internal_remote_connection_firewall_ingress" {
  name    = "${var.network_name}-firewall-ingress"
  network = google_compute_network.packer_network.name

  allow {
    protocol = "tcp"
    ports    = ["22", "3389"]
  }

  priority = 900

  direction = "INGRESS"
  # https://googlecloudplatform.github.io/iap-desktop/setup-iap/
  source_ranges = ["35.235.240.0/20"]
}