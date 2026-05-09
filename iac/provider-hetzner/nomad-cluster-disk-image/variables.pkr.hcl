variable "hetzner_api_token" {
  type      = string
  sensitive = true
}

variable "prefix" {
  type        = string
  description = "Resource name prefix (must end with '-')."
  default     = "maxicore-"
}

variable "location" {
  type        = string
  description = "Hetzner location for builder server (snapshots are global)."
  default     = "fsn1"
}

variable "base_image_slug" {
  type        = string
  description = "Hetzner Cloud Image slug for the base OS. ubuntu-24.04 or ubuntu-22.04."
  default     = "ubuntu-24.04"
}

variable "builder_server_type" {
  type        = string
  description = "Hetzner server type used to build snapshots (cpx21 = 3vCPU/4GB sufficient)."
  default     = "cpx21"
}

variable "builder_server_type_client" {
  type        = string
  description = "Server type for client (Firecracker-Host) snapshot — needs CCX for KVM/nested-virt."
  default     = "ccx13"
}

variable "consul_version" {
  type        = string
  description = "Consul package version (e.g. 1.18.1)."
  default     = "1.18.1"
}

variable "nomad_version" {
  type        = string
  description = "Nomad package version (e.g. 1.7.6)."
  default     = "1.7.6"
}
