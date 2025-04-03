terraform {
  required_version = ">= 1.5.0, < 1.6.0"

  backend "gcs" {
    prefix = "terraform/redis/state"
  }
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "6.28.0"
    }
  }
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}


# Enable the Service Networking API
resource "google_project_service" "service_networking" {
  service            = "servicenetworking.googleapis.com"
  disable_on_destroy = false
}

resource "time_sleep" "secrets_service_networking_api_wait_60_seconds" {
  depends_on = [google_project_service.service_networking]

  create_duration = "60s"
}


# Enable the Redis API
resource "google_project_service" "redis" {
  service            = "redis.googleapis.com"
  disable_on_destroy = false
}

resource "time_sleep" "redis_api_wait_60_seconds" {
  depends_on = [google_project_service.redis]

  create_duration = "60s"
}


# Get the default network resource
data "google_compute_network" "default" {
  name = var.network_name
}

# Get the default network resource
resource "google_compute_subnetwork" "default" {
  name                     = var.network_name
  region                   = var.gcp_region
  network                  = data.google_compute_network.default.id
  private_ip_google_access = true
}


# Allocate an IP address range for private services in the default network
resource "google_compute_global_address" "private_ip_alloc" {
  name          = "google-services-private-ip-alloc"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = data.google_compute_network.default.id
}

# Create the private connection for the default network
resource "google_service_networking_connection" "private_service_connection" {
  network                 = data.google_compute_network.default.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_alloc.name]

  depends_on = [
    google_project_service.service_networking
  ]
}

resource "google_redis_cluster" "redis_cluster_api" {
  name        = "${var.prefix}redis-cluster-api"
  shard_count = 2

  psc_configs {
    network = data.google_compute_network.default.id
  }

  region                  = var.gcp_region
  replica_count           = 1
  node_type               = "REDIS_STANDARD_SMALL"
  transit_encryption_mode = "TRANSIT_ENCRYPTION_MODE_DISABLED"
  authorization_mode      = "AUTH_MODE_DISABLED"

  deletion_protection_enabled = true

  zone_distribution_config {
    mode = "SINGLE_ZONE"
    zone = var.gcp_zone
  }

  depends_on = [
    google_network_connectivity_service_connection_policy.default,
    google_service_networking_connection.private_service_connection,
    google_project_service.redis
  ]
}

resource "google_network_connectivity_service_connection_policy" "default" {
  name          = "${var.prefix}redis-connection-policy"
  location      = var.gcp_region
  service_class = "gcp-memorystore-redis"
  description   = "my basic service connection policy"
  network       = data.google_compute_network.default.id
  psc_config {
    subnetworks = [google_compute_subnetwork.default.id]
  }
}


resource "google_secret_manager_secret_version" "redis_url" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}redis-url"
  secret_data = google_redis_cluster.redis_cluster_api.psc_connections[0].address
}