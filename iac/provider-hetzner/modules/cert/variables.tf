/**
 * Hetzner Cert Module — Variables
 */

variable "prefix" {
  type        = string
  description = "Resource name prefix (e.g. 'maxicore-')."
}

variable "domain_name" {
  type        = string
  description = "Primary domain (cert common-name; wildcard SAN is *.{domain_name})."
}

variable "acme_email" {
  type        = string
  description = "Email for Let's Encrypt account registration. Used for expiry notifications."
}

variable "hetzner_dns_token" {
  type        = string
  description = "Hetzner DNS API token (for ACME DNS-01 challenge)."
  sensitive   = true
}

variable "additional_sans" {
  type        = list(string)
  description = "Additional Subject Alternative Names (SANs) to include in the cert."
  default     = []
}

variable "min_days_remaining" {
  type        = number
  description = "Re-issue when fewer days remain until expiry. Let's Encrypt certs are valid 90d default 30 = renew at 60 days remaining."
  default     = 30
}

variable "recursive_nameservers" {
  type        = list(string)
  description = "Public recursive nameservers used for DNS-01 propagation checks."
  default     = ["1.1.1.1:53", "8.8.8.8:53"]
}

variable "upload_to_hcloud" {
  type        = bool
  description = "Upload the issued cert as a Hetzner Cloud Certificate (for Cloud LB HTTPS termination)."
  default     = true
}

variable "common_labels" {
  type        = map(string)
  description = "Common labels to attach to the Hetzner Cloud Certificate."
  default     = {}
}
