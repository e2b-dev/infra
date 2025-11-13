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

resource "google_project_service" "memory_store" {
  service            = "memorystore.googleapis.com"
  disable_on_destroy = false
}

resource "time_sleep" "redis_api_wait_60_seconds" {
  depends_on = [google_project_service.redis]

  create_duration = "60s"
}

resource "time_sleep" "memory_store_api_wait_60_seconds" {
  depends_on = [google_project_service.memory_store]

  create_duration = "60s"
}


# Get the default network resource
resource "google_compute_subnetwork" "default" {
  name                     = var.network_name
  region                   = var.gcp_region
  network                  = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
  private_ip_google_access = true
}


# Allocate an IP address range for private services in the default network
resource "google_compute_global_address" "private_ip_alloc" {
  name          = "google-services-private-ip-alloc"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
}

# Create the private connection for the default network
resource "google_service_networking_connection" "private_service_connection" {
  network                 = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_alloc.name]

  depends_on = [
    google_project_service.service_networking
  ]
}

# PSC policy for Valkey on default VPC in europe-west1
resource "google_network_connectivity_service_connection_policy" "valkey" {
  name          = "${var.prefix}memorystore-valkey-connection-policy"
  location      = var.gcp_region
  service_class = "gcp-memorystore"
  description   = "my basic service connection policy"
  network       = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
  psc_config {
    subnetworks = [google_compute_subnetwork.default.id]
  }
}

resource "google_memorystore_instance" "valkey_cluster" {
  project     = var.gcp_project_id
  location    = var.gcp_region
  instance_id = "${var.prefix}redis-valkey-cluster"

  engine_version = "VALKEY_8_0"
  mode           = "CLUSTER"

  desired_auto_created_endpoints {
    network    = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
    project_id = var.gcp_project_id
  }

  shard_count             = var.shard_count
  replica_count           = var.replica_count
  node_type               = "STANDARD_SMALL"
  transit_encryption_mode = "SERVER_AUTHENTICATION"
  authorization_mode      = "AUTH_DISABLED"

  zone_distribution_config {
    mode = "MULTI_ZONE"
  }

  deletion_protection_enabled = true

  maintenance_policy {
    weekly_maintenance_window {
      day = "SUNDAY"
      start_time {
        hours = 1
      }
    }
  }

  persistence_config {
    mode = "AOF"
    aof_config {
      append_fsync = "EVERY_SEC"
    }
  }

  depends_on = [
    google_network_connectivity_service_connection_policy.valkey,
    google_service_networking_connection.private_service_connection,
    google_project_service.memory_store,
    time_sleep.memory_store_api_wait_60_seconds
  ]
}


resource "google_redis_cluster" "redis_cluster_api" {
  name        = "${var.prefix}redis-cluster-api"
  shard_count = 1

  psc_configs {
    network = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
  }

  region                  = var.gcp_region
  replica_count           = 1
  node_type               = "REDIS_STANDARD_SMALL"
  transit_encryption_mode = "TRANSIT_ENCRYPTION_MODE_DISABLED"
  authorization_mode      = "AUTH_MODE_DISABLED"

  deletion_protection_enabled = true

  zone_distribution_config {
    mode = "MULTI_ZONE"
  }

  persistence_config {
    mode = "AOF"
    aof_config {
      append_fsync = "EVERYSEC"
    }
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
  network       = "projects/${var.gcp_project_id}/global/networks/${var.network_name}"
  psc_config {
    subnetworks = [google_compute_subnetwork.default.id]
  }
}


resource "google_secret_manager_secret_version" "redis_url" {
  secret      = "projects/${var.gcp_project_id}/secrets/${var.prefix}redis-url"
  secret_data = google_redis_cluster.redis_cluster_api.psc_connections[0].address
}

locals {
  redis_connection = google_memorystore_instance.valkey_cluster.endpoints[0].connections[0].psc_auto_connection[0]
}

resource "google_secret_manager_secret_version" "redis_secure_cluster_url_secret_version" {
  secret      = var.redis_secure_cluster_url_secret_version.secret
  secret_data = "${local.redis_connection.ip_address}:${local.redis_connection.port}"
}

resource "google_secret_manager_secret_version" "redis_tls_ca_base64" {
  secret      = var.redis_tls_ca_base64_secret_version.secret
  secret_data = base64encode(google_memorystore_instance.valkey_cluster.managed_server_ca[0].ca_certs[0].certificates[0])
}
