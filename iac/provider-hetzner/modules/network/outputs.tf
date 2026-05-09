output "network_id"          { value = hcloud_network.maxicore_prod.id }
output "network_ip_range"    { value = hcloud_network.maxicore_prod.ip_range }
output "cloud_subnet_id"     { value = hcloud_network_subnet.cloud.id }
output "vswitch_subnet_id"   { value = try(hcloud_network_subnet.vswitch[0].id, null) }
