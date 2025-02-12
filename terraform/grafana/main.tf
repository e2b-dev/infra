terraform {
  backend "gcs" {
    prefix = "terraform/grafana/state"
  }
  required_providers {
    grafana = {
      source = "grafana/grafana"
    }
    google = {
      source = "hashicorp/google"
    }
  }
}


data "google_secret_manager_secret_version" "grafana_cloud_access_policy_token" {
  secret  = "${var.prefix}grafana-api-key"
  project = var.gcp_project_id
}

provider "grafana" {
  alias                     = "cloud"
  cloud_access_policy_token = data.google_secret_manager_secret_version.grafana_cloud_access_policy_token.secret_data
}

resource "grafana_cloud_stack" "e2b_stack" {
  provider = grafana.cloud

  name = var.gcp_project_id
  # (must be a lowercase alphanumeric string and must start with a letter.)
  # cannot use var.prefix because it contains -
  slug        = "e2bstack"
  region_slug = "us"
}

data "google_secret_manager_secret_version" "grafana_username" {
  secret  = "${var.prefix}grafana-username"
  project = var.gcp_project_id
  # # always get the first version so that when we can re run terraform plan or terraform apply 
  # # without errors 
  # │Error: error retrieving available secret manager secret version access: 
  # googleapi: Error 400: Secret Version [projects/402446661531/secrets/e2b-grafana-username/versions/4] is in DESTROYED state.
  version = "1"

}

resource "google_secret_manager_secret_version" "grafana_username" {
  # "projects/402446661531/secrets/e2b-grafana-username/versions/1"
  # we want to get the secret name without the version
  # no field exports this so we use regex to get it
  secret      = regex("(.*)/versions/[0-9]+", data.google_secret_manager_secret_version.grafana_username.name)[0]
  secret_data = grafana_cloud_stack.e2b_stack.id
}


data "grafana_cloud_organization" "current" {
  id       = var.grafana_cloud_organization_id
  provider = grafana.cloud

}

resource "grafana_cloud_access_policy" "otel_collector" {
  provider = grafana.cloud

  region       = "us"
  name         = "otel-collector"
  display_name = "Otel Collector"

  # scopes = ["metrics:read", "logs:read"]
  # metrics:writelogs:writetraces:writeprofiles:write
  scopes = ["metrics:write", "logs:write", "traces:write", "profiles:write"]

  realm {
    type       = "org"
    identifier = data.grafana_cloud_organization.current.id

    label_policy {
      selector = "{namespace=\"default\"}"
    }
  }
}

resource "grafana_cloud_access_policy_token" "otel_collector" {
  provider         = grafana.cloud
  region           = "us"
  access_policy_id = grafana_cloud_access_policy.otel_collector.policy_id
  name             = "otel-collector"
  display_name     = "Otel Collector"
}


data "google_secret_manager_secret_version" "otel_collector_token" {
  secret  = "${var.prefix}grafana-otel-collector-token"
  project = var.gcp_project_id
  # # always get the first version so that when we can re run terraform plan or terraform apply 
  # # without errors 
  # │Error: error retrieving available secret manager secret version access: 
  # googleapi: Error 400: Secret Version [projects/402446661531/secrets/e2b-grafana-username/versions/4] is in DESTROYED state.
  version = "1"

}

resource "google_secret_manager_secret_version" "otel_collector_token" {
  secret      = regex("(.*)/versions/[0-9]+", data.google_secret_manager_secret_version.otel_collector_token.name)[0]
  secret_data = grafana_cloud_access_policy_token.otel_collector.token
}
