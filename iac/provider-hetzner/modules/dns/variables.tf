/**
 * Hetzner DNS Module — Variables
 */

variable "domain_root" {
  type        = string
  description = "Root domain (zone name in Hetzner DNS, e.g. helix12.eu)."
}

variable "domain_name" {
  type        = string
  description = "Primary domain or subdomain for this deployment (e.g. sandbox.helix12.eu)."
}

variable "lb_ipv4" {
  type        = string
  description = "Hetzner Cloud Load Balancer IPv4 address (used when lb_hostname is empty, or for apex A-record)."
  default     = ""
}

variable "lb_ipv6" {
  type        = string
  description = "Hetzner Cloud Load Balancer IPv6 address."
  default     = ""
}

variable "lb_hostname" {
  type        = string
  description = "Hetzner Cloud Load Balancer hostname (preferred over IP for portability). E.g. {prefix}-lb.fsn1.hetzner.cloud."
  default     = ""
}

variable "create_apex_a_record" {
  type        = bool
  description = "Create A/AAAA record for the domain itself (instead of just CNAME)."
  default     = false
}

variable "additional_records" {
  type = map(object({
    name  = string
    type  = string
    value = string
    ttl   = number
  }))
  description = "Additional DNS records (e.g. MX, TXT for SPF/DKIM, custom CNAMEs)."
  default     = {}
}
