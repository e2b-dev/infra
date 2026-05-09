output "primary_ip" {
  description = "Private IP of the Redis primary."
  value       = tolist(hcloud_server.primary.network)[0].ip
}

output "primary_endpoint" {
  description = "Redis primary endpoint (host:port) for clients."
  value       = "${tolist(hcloud_server.primary.network)[0].ip}:${var.port}"
}

output "replica_ips" {
  description = "Private IPs of Redis replicas. Empty when replica_size=0."
  value       = [for s in hcloud_server.replica : tolist(s.network)[0].ip]
}

output "redis_url" {
  description = "Full Redis URL with auth (sensitive)."
  value       = "redis://default:${var.auth_token}@${tolist(hcloud_server.primary.network)[0].ip}:${var.port}"
  sensitive   = true
}
