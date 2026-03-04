output "vpc_id" {
  value = module.vpc.vpc_id
}

output "vpc_private_subnets" {
  value = module.vpc.private_subnets
}

output "vpc_public_subnet_ids" {
  value = local.default_public_subnet_ids
}

output "elasticache_subnet_group_name" {
  value = module.vpc.elasticache_subnet_group_name
}

output "instance_connect_security_group_id" {
  description = "The security group ID for instance connect endpoints (if enabled)"
  value       = var.use_instance_connect ? aws_security_group.connect_endpoint[0].id : null
}
