variable "prefix" { type = string }
variable "node_pool_name" {
  type    = string
  default = "default"
}
variable "cluster_size" {
  type    = number
  default = 1
}
variable "server_type" {
  type    = string
  default = "ccx33"
}
variable "location" {
  type    = string
  default = "fsn1"
}
variable "image_id" {
  type    = string
  default = ""
}
variable "image_family_prefix" { type = string }
variable "ssh_key_ids" {
  type    = list(number)
  default = []
}
variable "firewall_ids" {
  type    = list(number)
  default = []
}
variable "network_id" { type = number }
variable "subnet_cidr" { type = string }
variable "subnet_offset" {
  type    = number
  default = 50
}
variable "scripts_path" {
  type    = string
  default = ""
}

# Firecracker-host specifics (1:1 Manus pattern)
variable "fc_kernels_bucket" { type = string }
variable "fc_versions_bucket" { type = string }
variable "fc_env_pipeline_bucket" { type = string }
variable "fc_busybox_bucket" { type = string }
variable "node_labels" {
  type    = list(string)
  default = []
}
variable "base_hugepages_percentage" {
  type    = number
  default = 80
}

variable "cluster_tag_name" { type = string }
variable "cluster_tag_value" { type = string }
variable "setup_bucket_name" { type = string }
variable "object_store_url" { type = string }
variable "object_store_access_key" {
  type      = string
  sensitive = true
}
variable "object_store_secret_key" {
  type      = string
  sensitive = true
}
variable "consul_acl_token" {
  type      = string
  sensitive = true
}
variable "consul_gossip_encryption_key" {
  type      = string
  sensitive = true
}
variable "nomad_acl_token" {
  type      = string
  sensitive = true
}

variable "common_labels" {
  type    = map(string)
  default = {}
}
variable "allow_force_destroy" {
  type    = bool
  default = false
}
