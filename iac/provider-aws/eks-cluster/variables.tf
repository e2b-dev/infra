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
  description = "Hugepages percentage for client nodes (applied to all Karpenter-managed nodes)"
  type        = number
  default     = 80
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

variable "public_access_cidrs" {
  description = "CIDR blocks allowed to access the EKS API endpoint publicly. Restrict for production."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "eks_cluster_log_types" {
  description = "EKS control plane log types to enable"
  type        = list(string)
  default     = ["api", "audit", "authenticator", "controllerManager", "scheduler"]
}

variable "eks_log_retention_days" {
  description = "CloudWatch log group retention in days for EKS cluster logs"
  type        = number
  default     = 90
}

variable "client_consolidation_after" {
  description = "Karpenter consolidation delay for client NodePool (prevents thrashing for bursty sandboxes)"
  type        = string
  default     = "300s"
}

variable "build_consolidation_after" {
  description = "Karpenter consolidation delay for build NodePool (batch-style, fast consolidation)"
  type        = string
  default     = "60s"
}

variable "cache_disk_iops" {
  description = "Provisioned IOPS for cache EBS volume (gp3 baseline: 3000, recommended: 6000 for high sandbox density)"
  type        = number
  default     = 6000
}

variable "cache_disk_throughput_mbps" {
  description = "Provisioned throughput in MB/s for cache EBS volume (gp3 baseline: 125, recommended: 400)"
  type        = number
  default     = 400
}

variable "bootstrap_instance_type" {
  description = "Instance type for bootstrap managed node group (Karpenter controller + system pods)"
  type        = string
  default     = "t3.large"
}

variable "temporal_enabled" {
  description = "Whether Temporal is enabled (affects bootstrap node pool sizing)"
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
  default     = {}
}
