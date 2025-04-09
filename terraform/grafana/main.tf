terraform {
  required_providers {
    grafana = {
      source  = "grafana/grafana"
      version = "3.18.3"
    }
  }
}

data "google_secret_manager_secret_version" "grafana_api_key" {
  secret  = "projects/${var.gcp_project_id}/secrets/${var.prefix}grafana-api-key"
  project = var.gcp_project_id
}

provider "grafana" {
  alias                     = "cloud"
  cloud_access_policy_token = data.google_secret_manager_secret_version.grafana_api_key.secret_data
}

module "grafana_cloud" {
  source = "./cloud"
  count  = var.grafana_managed ? 1 : 0
  providers = {
    grafana = grafana.cloud
  }

  gcp_project_id = var.gcp_project_id
  gcp_region     = var.gcp_region
  prefix         = var.prefix
}

provider "grafana" {
  alias = "datasource"
  url   = var.grafana_managed ? module.grafana_cloud[0].stack_url : "https://nonexisting.grafana.com"
  auth  = var.grafana_managed ? module.grafana_cloud[0].service_account_token : ""
}

module "grafana_stack" {
  source = "./stack"
  count  = var.grafana_managed ? 1 : 0
  providers = {
    grafana = grafana.datasource
  }

  gcp_project_id = var.gcp_project_id
  domain_name    = var.domain_name

  panels_directory_name     = var.panels_directory_name
  dashboards_directory_name = var.dashboards_directory_name
}