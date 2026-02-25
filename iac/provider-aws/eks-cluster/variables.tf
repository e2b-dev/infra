variable "cluster_name" {
  description = "Name of the EKS cluster"
  type        = string
}

variable "kubernetes_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  default     = "1.31"
}

variable "vpc_id" {
  description = "VPC ID where the EKS cluster will be deployed"
  type        = string
}

variable "subnet_ids" {
  description = "Subnet IDs for the EKS cluster (public subnets for nodes)"
  type        = list(string)
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for internal services"
  type        = list(string)
}

variable "cluster_sg_id" {
  description = "Security group ID for EKS nodes"
  type        = string
}

variable "iam_instance_profile_name" {
  description = "IAM instance profile for nodes (existing infra profile)"
  type        = string
}

variable "client_instance_types" {
  description = "Instance types for the client (orchestrator) Karpenter NodePool"
  type        = list(string)
  default     = ["c8i.2xlarge", "c8i.4xlarge", "c8i.8xlarge"]
}

variable "build_instance_types" {
  description = "Instance types for the build (template-manager) Karpenter NodePool"
  type        = list(string)
  default     = ["c8i.2xlarge", "c8i.4xlarge", "c8i.8xlarge"]
}

variable "client_capacity_types" {
  description = "Capacity types for client NodePool (on-demand, spot). Spot enables lower-cost scale-to-zero."
  type        = list(string)
  default     = ["on-demand", "spot"]
}

variable "eks_ami_id" {
  description = "Custom AMI ID for EKS nodes (must have kubelet + nested virtualization support)"
  type        = string
}

variable "boot_disk_size_gb" {
  description = "Boot EBS volume size in GB"
  type        = number
  default     = 100
}

variable "cache_disk_size_gb" {
  description = "Cache EBS volume size in GB"
  type        = number
  default     = 500
}

variable "client_hugepages_percentage" {
  description = "Hugepages percentage for client nodes"
  type        = number
  default     = 80
}

variable "build_hugepages_percentage" {
  description = "Hugepages percentage for build nodes"
  type        = number
  default     = 60
}

variable "efs_dns_name" {
  description = "EFS DNS name for shared cache mount"
  type        = string
  default     = ""
}

variable "efs_mount_path" {
  description = "Mount path for EFS shared cache"
  type        = string
  default     = "/orchestrator/shared-store"
}

variable "karpenter_version" {
  description = "Karpenter Helm chart version"
  type        = string
  default     = "1.6.0"
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}

variable "prefix" {
  description = "Resource name prefix"
  type        = string
}
