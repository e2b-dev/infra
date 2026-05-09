variable "prefix" { type = string }
variable "node_pool_name" {
  type    = string
  default = "clickhouse"
}
variable "cluster_size" {
  type    = number
  default = 1
}
variable "server_type" {
  type    = string
  default = "cpx41"
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
  default = 30
}
variable "scripts_path" {
  type    = string
  default = ""
}
variable "data_volume_size_gb" {
  type    = number
  default = 100
}

variable "cluster_tag_name" { type = string }
variable "cluster_tag_value" { type = string }
variable "setup_bucket_name" { type = string }
variable "clickhouse_backups_bucket" { type = string }
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
