terraform {
  required_version = ">=1.1.9"
  backend "gcs" {
    bucket = "e2b-terraform-state"
    prefix = "terraform/orchestration/state"
  }
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "2.16.0"
    }
  }
}

provider "docker" {}

provider "google-beta" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

provider "google" {
  project = var.gcp_project_id
  region  = var.gcp_region
  zone    = var.gcp_zone
}

resource "google_service_account" "infra_instances_service_account" {
  account_id   = "infra-instances-1"
  display_name = "Infra Instances Service Account"
}

module "github-tf" {
  source = "./github-tf"

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  gcp_zone       = var.gcp_zone

  github_organization = var.github_organization
  github_repository   = var.github_repository

}

module "cluster" {
  source = "./packages/cluster"

  gcp_project_id = var.gcp_project_id

  server_cluster_size = var.server_cluster_size
  client_cluster_size = var.client_cluster_size

  server_machine_type = var.server_machine_type
  client_machine_type = var.client_machine_type

  session_proxy_service_name = var.session_proxy_service_name

  logs_health_proxy_port = var.logs_health_proxy_port
  logs_proxy_port        = var.logs_proxy_port

  session_proxy_port           = var.session_proxy_port
  client_proxy_health_port     = var.client_proxy_health_port
  client_proxy_port            = var.client_proxy_port
  api_port                     = var.api_port
  google_service_account_email = google_service_account.infra_instances_service_account.email
}

data "google_compute_global_address" "orch_server_consul_ip" {
  name = "orch-server-consul-ip"
}

data "google_secret_manager_secret_version" "consul_acl_token" {
  secret = "consul-secret-id"
}

data "google_compute_global_address" "orch_server_ip" {
  name = "orch-server-nomad-ip"
}

data "google_secret_manager_secret_version" "nomad_acl_token" {
  secret = "nomad-secret-id"
}

provider "nomad" {
  address   = "http://${data.google_compute_global_address.orch_server_ip.address}"
  secret_id = data.google_secret_manager_secret_version.nomad_acl_token.secret_data
}

data "google_secret_manager_secret_version" "lightstep_api_key" {
  secret = "lightstep-api-key"
}

data "google_secret_manager_secret_version" "betterstack_logs_api_key" {
  secret = "betterstack-logs-api-key"
}

provider "consul" {
  address = "http://${data.google_compute_global_address.orch_server_consul_ip.address}"
  token   = data.google_secret_manager_secret_version.consul_acl_token.secret_data
}

resource "consul_acl_policy" "agent" {
  name  = "agent"
  rules = <<-RULE
    key_prefix "" {
      policy = "deny"
    }
    RULE
}

resource "consul_acl_token_policy_attachment" "attachment" {
  token_id = "00000000-0000-0000-0000-000000000002"
  policy   = consul_acl_policy.agent.name
}

module "telemetry" {
  source = "./packages/telemetry"

  logs_health_proxy_port   = var.logs_health_proxy_port
  logs_proxy_port          = var.logs_proxy_port
  lightstep_api_key        = data.google_secret_manager_secret_version.lightstep_api_key.secret_data
  betterstack_logs_api_key = data.google_secret_manager_secret_version.betterstack_logs_api_key.secret_data
  gcp_zone                 = var.gcp_zone
}

module "session_proxy" {
  source = "./packages/session-proxy"

  client_cluster_size        = var.client_cluster_size
  gcp_zone                   = var.gcp_zone
  session_proxy_service_name = var.session_proxy_service_name

  session_proxy_port = var.session_proxy_port
}

module "client_proxy" {
  source = "./packages/client-proxy"

  gcp_zone                   = var.gcp_zone
  session_proxy_service_name = var.session_proxy_service_name

  client_proxy_port        = var.client_proxy_port
  client_proxy_health_port = var.client_proxy_health_port
}

data "google_storage_bucket" "e2b-envs-docker-context" {
  name = "e2b-envs-docker-context"
}


resource "google_service_account_key" "google_service_key" {
  service_account_id = google_service_account.infra_instances_service_account.name
}


module "api" {
  source = "./packages/api"

  gcp_zone = var.gcp_zone

  logs_proxy_address            = "http://${module.cluster.logs_proxy_ip}"
  nomad_address                 = "http://${module.cluster.server_proxy_ip}"
  nomad_token                   = data.google_secret_manager_secret_version.nomad_acl_token.secret_data
  consul_token                  = data.google_secret_manager_secret_version.consul_acl_token.secret_data
  api_port                      = var.api_port
  environment                   = var.environment
  bucket_name                   = data.google_storage_bucket.e2b-envs-docker-context.name
  google_service_account_secret = google_service_account_key.google_service_key.private_key
}
