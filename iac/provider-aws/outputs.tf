output "vpc_id" {
  value = module.network.vpc_id
}

output "alb_dns_name" {
  value = module.load_balancer.alb_dns_name
}

output "nlb_dns_name" {
  value = module.load_balancer.nlb_dns_name
}

output "core_ecr_repository_url" {
  value = module.init.core_repository_url
}

output "eks_cluster_endpoint" {
  value = module.eks_cluster.cluster_endpoint
}

output "eks_cluster_name" {
  value = module.eks_cluster.cluster_name
}
