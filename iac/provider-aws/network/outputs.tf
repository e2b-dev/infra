output "vpc_id" {
  value = aws_vpc.main.id
}

output "public_subnet_ids" {
  value = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  value = aws_subnet.private[*].id
}

output "eks_nodes_security_group_id" {
  value = aws_security_group.eks_nodes.id
}

output "alb_security_group_id" {
  value = aws_security_group.alb.id
}

output "rds_security_group_id" {
  value = aws_security_group.rds.id
}

output "elasticache_security_group_id" {
  value = aws_security_group.elasticache.id
}

output "efs_security_group_id" {
  value = aws_security_group.efs.id
}

output "vpc_flow_log_group_name" {
  value = var.enable_vpc_flow_logs ? aws_cloudwatch_log_group.vpc_flow_logs[0].name : ""
}
