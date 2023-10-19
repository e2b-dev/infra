terraform {
  required_version = ">= 1.1.9"
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "2.16.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.5.1"
    }
  }
}

data "docker_registry_image" "api_image" {
  name = var.image_name
}

resource "docker_image" "api_image" {
  name          = data.docker_registry_image.api_image.name
  pull_triggers = [data.docker_registry_image.api_image.sha256_digest]
}

data "google_secret_manager_secret_version" "supabase_connection_string" {
  secret = "supabase-connection-string"
}

data "google_secret_manager_secret_version" "posthog_api_key" {
  secret = "posthog-api-key"
}

data "google_secret_manager_secret_version" "api_admin_key" {
  secret = "api-admin-key"
}

resource "random_password" "api_secret" {
  length = 32
}

resource "google_secret_manager_secret" "api_secret" {
  secret_id = "api-secret"

  replication {
    automatic = true
  }
}

resource "google_secret_manager_secret_version" "api_secret_value" {
  secret = google_secret_manager_secret.api_secret.id

  secret_data = random_password.api_secret.result
}

resource "nomad_job" "api" {
  jobspec = file("${path.module}/api.hcl")

  hcl2 {
    enabled = true
    vars = {
      gcp_zone                   = var.gcp_zone
      api_port_name              = var.api_port.name
      api_port_number            = var.api_port.port
      image_name                 = resource.docker_image.api_image.repo_digest
      supabase_connection_string = data.google_secret_manager_secret_version.supabase_connection_string.secret_data
      posthog_api_key            = data.google_secret_manager_secret_version.posthog_api_key.secret_data
      api_admin_key              = data.google_secret_manager_secret_version.api_admin_key.secret_data
      logs_proxy_address         = var.logs_proxy_address
      nomad_address              = var.nomad_address
      nomad_token                = var.nomad_token
      consul_token               = var.consul_token
      environment                = var.environment
      bucket_name                = var.bucket_name
      api_secret                 = resource.random_password.api_secret.result
    }
  }
}
