output "cluster_endpoint" {
  description = "EKS cluster API endpoint"
  value       = module.eks.cluster_endpoint
}

output "cluster_certificate_authority_data" {
  description = "EKS cluster CA certificate (base64)"
  value       = module.eks.cluster_certificate_authority_data
}

output "cluster_name" {
  description = "EKS cluster name"
  value       = module.eks.cluster_name
}

output "oidc_provider_arn" {
  description = "OIDC provider ARN for IRSA"
  value       = module.eks.oidc_provider_arn
}

output "node_iam_role_arn" {
  description = "IAM role ARN for EKS nodes"
  value       = module.eks.eks_managed_node_groups["bootstrap"].iam_role_arn
}

output "karpenter_namespace" {
  description = "Namespace where Karpenter is installed"
  value       = "kube-system"
}

output "shared_chunk_cache_path" {
  description = "Shared chunk cache path (EFS mount)"
  value       = var.efs_dns_name != "" ? "${var.efs_mount_path}/chunks-cache" : ""
}

output "cluster_security_group_id" {
  description = "EKS cluster security group ID"
  value       = module.eks.cluster_security_group_id
}

output "node_security_group_id" {
  description = "EKS node security group ID"
  value       = module.eks.node_security_group_id
}
