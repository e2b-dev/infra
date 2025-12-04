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

variable "gcp_zone" {
  type = string
}

variable "network_name" {
  type    = string
  default = "default"
}

// https://registry.terraform.io/providers/hashicorp/google/6.38.0/docs/resources/memorystore_instance#shard_count-1
variable "shard_count" {
  type    = number
  default = 1
}

// https://registry.terraform.io/providers/hashicorp/google/6.38.0/docs/resources/memorystore_instance#replica_count-1
variable "replica_count" {
  type    = number
  default = 1
}

variable "redis_cluster_url_secret_version" {
  type = any
}

variable "redis_tls_ca_base64_secret_version" {
  type = any
}