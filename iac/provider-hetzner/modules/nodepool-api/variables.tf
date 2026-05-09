variable "prefix" {
  type        = string
  description = "Resource name prefix."
}

variable "node_pool_name" {
  type        = string
  description = "Logical pool name (used in cluster-tags and cloud-init)."
  default     = "api"
}

variable "cluster_size" {
  type        = number
  description = "Number of API server nodes."
  default     = 1
}

variable "server_type" {
  type        = string
  description = "Hetzner Cloud Server type. Default cpx41 = 8vCPU/16GB."
  default     = "cpx41"
}

variable "location" {
  type        = string
  description = "Hetzner location (fsn1, nbg1, hel1)."
  default     = "fsn1"
}

variable "image_id" {
  type        = string
  description = "Explicit Hetzner Cloud Image ID. Empty = auto-discover via with_selector."
  default     = ""
}

variable "image_family_prefix" {
  type        = string
  description = "Image family prefix for snapshot lookup (Packer label)."
}

variable "ssh_key_ids" {
  type        = list(number)
  description = "SSH key IDs to attach to all servers."
  default     = []
}

variable "firewall_ids" {
  type        = list(number)
  description = "Hetzner Cloud Firewall IDs (cluster-internal + public-ingress)."
  default     = []
}

variable "network_id" {
  type        = number
  description = "Hetzner Cloud Network ID."
}

variable "subnet_cidr" {
  type        = string
  description = "Cloud Subnet CIDR for IP allocation."
}

variable "subnet_offset" {
  type        = number
  description = "Offset into subnet for IP allocation (e.g. 10 = .11, .12, ...)."
  default     = 10
}

variable "scripts_path" {
  type        = string
  description = "Path to cloud-init scripts (default: module-local scripts/)."
  default     = ""
}

# Cluster bootstrap inputs (from init module)
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
variable "consul_dns_request_token" {
  type      = string
  sensitive = true
  default   = ""
}
variable "nomad_acl_token" {
  type      = string
  sensitive = true
}
variable "loki_bucket" {
  type    = string
  default = ""
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

variable "allow_force_destroy" {
  type    = bool
  default = false
}
