output "lb_id" {
  description = "Hetzner Cloud LB ID."
  value       = hcloud_load_balancer.main.id
}

output "lb_name" {
  description = "Hetzner Cloud LB name."
  value       = hcloud_load_balancer.main.name
}

output "lb_ipv4" {
  description = "Public IPv4 address of the LB."
  value       = hcloud_load_balancer.main.ipv4
}

output "lb_ipv6" {
  description = "Public IPv6 address of the LB."
  value       = hcloud_load_balancer.main.ipv6
}

output "lb_private_ipv4" {
  description = "Private IPv4 of the LB inside the Cloud Network."
  value       = hcloud_load_balancer_network.main.ip
}

output "lb_hostname" {
  description = "Computed LB hostname for DNS CNAME records (NX.2.2 dns module input)."
  value       = "${hcloud_load_balancer.main.name}.${hcloud_load_balancer.main.location}.hetzner.cloud"
}
