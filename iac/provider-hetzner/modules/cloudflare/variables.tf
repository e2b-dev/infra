variable "domain_root" {
  type        = string
  description = "Root domain (e.g. helix12.eu)."
}

variable "domain_name" {
  type        = string
  description = "Primary domain or subdomain (e.g. sandbox.helix12.eu)."
}

variable "cloudflare_api_token" {
  type        = string
  description = "Cloudflare API token."
  sensitive   = true
}
