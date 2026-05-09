/**
 * Hetzner Init Module — Variables
 */

variable "prefix" {
  type        = string
  description = "Resource name prefix (e.g. 'maxicore-')."
}

variable "bucket_prefix" {
  type        = string
  description = "Object Storage bucket name prefix."
}

variable "environment" {
  type        = string
  description = "Deployment environment (dev|staging|prod)."
}

variable "region" {
  type        = string
  description = "Hetzner Cloud region (e.g. fsn1)."
}

variable "network_zone" {
  type        = string
  description = "Hetzner network zone (e.g. eu-central)."
}

variable "domain_name" {
  type        = string
  description = "Primary domain or subdomain."
}

variable "domain_root" {
  type        = string
  description = "Root domain (e.g. helix12.eu from sandbox.helix12.eu)."
}

variable "common_labels" {
  type        = map(string)
  description = "Common labels for all resources."
  default     = {}
}

variable "object_store_url" {
  type        = string
  description = "Hetzner Object Storage endpoint URL."
}

variable "hetzner_api_token" {
  type        = string
  description = "Hetzner Cloud API token."
  sensitive   = true
}

variable "ssh_key_ids" {
  type        = list(number)
  description = "Hetzner Cloud SSH key IDs."
  default     = []
}

variable "allow_force_destroy" {
  type        = bool
  description = "Allow force-destroy on buckets and resources."
  default     = false
}
