/**
 * Hetzner Network Module — Variables
 */

variable "prefix" {
  type        = string
  description = "Resource name prefix (e.g. 'maxicore-')."
}

variable "network_zone" {
  type        = string
  description = "Hetzner network zone (e.g. eu-central)."
}

variable "cloud_cidr" {
  type        = string
  description = "Top-level Cloud Network CIDR (e.g. 10.0.0.0/8)."
}

variable "cloud_subnet_cidr" {
  type        = string
  description = "Cloud Subnet CIDR for Cloud Servers (e.g. 10.0.1.0/24)."
}

variable "vswitch_cidr" {
  type        = string
  description = "vSwitch Subnet CIDR for Cloud↔Robot bridging (e.g. 10.10.0.0/24)."
}

variable "vswitch_id" {
  type        = number
  description = "Existing Hetzner vSwitch ID. 0 = no vSwitch (skip Robot integration)."
  default     = 0
}

variable "vlan_id" {
  type        = number
  description = "VLAN ID for the vSwitch (Hetzner range 4000-4091)."
  default     = 4000
}

variable "common_labels" {
  type        = map(string)
  description = "Common labels applied to all resources."
  default     = {}
}

variable "allow_force_destroy" {
  type        = bool
  description = "If true, network can be force-destroyed."
  default     = false
}

variable "management_cidrs" {
  type        = list(string)
  description = "Source CIDRs allowed for SSH management. Empty list disables SSH ingress."
  default     = []
}

variable "allow_sandbox_internal_cidrs" {
  type        = list(string)
  description = "Internal CIDRs that sandboxes are allowed to reach (egress allowlist)."
  default     = []
}
