/**
 * Hetzner Network Module — Outputs
 */

output "cloud_network_id" {
  description = "Hetzner Cloud Network ID."
  value       = hcloud_network.main.id
}

output "cloud_network_ip_range" {
  description = "Hetzner Cloud Network IP range."
  value       = hcloud_network.main.ip_range
}

output "cloud_subnet_id" {
  description = "Cloud Subnet ID (for Cloud Servers)."
  value       = hcloud_network_subnet.cloud.id
}

output "cloud_subnet_ip_range" {
  description = "Cloud Subnet IP range."
  value       = hcloud_network_subnet.cloud.ip_range
}

output "vswitch_subnet_id" {
  description = "vSwitch Subnet ID. null when vswitch_id = 0."
  value       = try(hcloud_network_subnet.vswitch[0].id, null)
}

output "vswitch_subnet_ip_range" {
  description = "vSwitch Subnet IP range. null when vswitch_id = 0."
  value       = try(hcloud_network_subnet.vswitch[0].ip_range, null)
}

output "firewall_public_ingress_id" {
  description = "Cloud Firewall ID for public-ingress rules."
  value       = hcloud_firewall.public_ingress.id
}

output "firewall_cluster_internal_id" {
  description = "Cloud Firewall ID for cluster-internal traffic."
  value       = hcloud_firewall.cluster_internal.id
}

output "firewall_sandbox_egress_id" {
  description = "Cloud Firewall ID for sandbox egress restrictions."
  value       = hcloud_firewall.sandbox_egress.id
}
