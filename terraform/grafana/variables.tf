variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "gcp_project_id" {
  type = string
}

variable "gcp_region" {
  type = string
}

variable "domain_name" {
  type = string
}

variable "directory_path" {
  description = "Path to the directory containing files"
  type        = string
  default     = "./panels"
}

variable "gcp_to_grafana_regions" {
  description = "Mapping of GCP regions to Grafana stack regions"
  type        = map(string)
  default = {
    "us-central1"             = "us"
    "us-east1"                = "prod-us-east-0"
    "us-east4"                = "prod-us-east-0"
    "us-east5"                = "prod-us-east-0"
    "us-west1"                = "prod-us-west-0"
    "us-west2"                = "prod-us-west-0"
    "us-west3"                = "prod-us-west-0"
    "us-west4"                = "prod-us-west-0"
    "us-south1"               = "us"
    "northamerica-northeast1" = "prod-ca-east-0"
    "northamerica-northeast2" = "prod-ca-east-0"
    "europe-west1"            = "eu"
    "europe-west2"            = "prod-eu-west-2"
    "europe-west3"            = "prod-eu-west-3"
    "europe-west4"            = "eu"
    "europe-west6"            = "eu"
    "europe-central2"         = "eu"
    "europe-north1"           = "prod-eu-north-0"
    "asia-east1"              = "prod-ap-southeast-1"
    "asia-east2"              = "prod-ap-southeast-1"
    "asia-northeast1"         = "prod-ap-northeast-0"
    "asia-northeast2"         = "prod-ap-northeast-0"
    "asia-northeast3"         = "prod-ap-northeast-0"
    "asia-south1"             = "prod-ap-south-0"
    "asia-south2"             = "prod-ap-south-0"
    "asia-southeast1"         = "prod-ap-southeast-0"
    "asia-southeast2"         = "prod-ap-southeast-2"
    "australia-southeast1"    = "prod-au-southeast-1"
    "australia-southeast2"    = "au"
    "southamerica-east1"      = "prod-sa-east-0"
    "southamerica-west1"      = "prod-sa-east-0"
    "me-central1"             = "prod-me-central-0"
    "me-central2"             = "prod-me-central-0"
    "me-west1"                = "prod-me-central-0"
  }
}

