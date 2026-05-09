variable "prefix" {
  type = string
}

variable "server_type" {
  type        = string
  description = "Hetzner Cloud Server type. cx22=2vCPU/4GB (dev), cpx31=4vCPU/8GB (prod)."
  default     = "cx22"
}

variable "location" {
  type    = string
  default = "fsn1"
}

variable "image_id" {
  type    = string
  default = ""
}

variable "image_family_prefix" {
  type        = string
  description = "Image family prefix for snapshot lookup."
  default     = ""
}

variable "ssh_key_ids" {
  type    = list(number)
  default = []
}

variable "firewall_ids" {
  type    = list(number)
  default = []
}

variable "network_id" {
  type = number
}

variable "subnet_cidr" {
  type = string
}

variable "subnet_offset" {
  type        = number
  description = "Offset into subnet_cidr for primary IP."
  default     = 60
}

variable "scripts_path" {
  type    = string
  default = ""
}

variable "port" {
  type    = number
  default = 6379
}

variable "auth_token" {
  type        = string
  description = "Redis AUTH password (sensitive)."
  sensitive   = true
}

variable "replica_size" {
  type        = number
  description = "Number of replicas for HA. 0 = single-primary only."
  default     = 0
}

variable "data_volume_size_gb" {
  type    = number
  default = 50
}

variable "common_labels" {
  type    = map(string)
  default = {}
}

variable "allow_force_destroy" {
  type    = bool
  default = false
}
