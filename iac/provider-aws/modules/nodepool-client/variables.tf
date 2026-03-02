variable "prefix" {
  type = string
}

variable "name" {
  type    = string
  default = "client"
}

variable "aws_account_id" {
  type = string
}

variable "cluster_tag_name" {
  type = string
}

variable "cluster_tag_value" {
  type = string
}

variable "cluster_node_policy_arn" {
  type        = string
  description = "ARN of the base cluster node IAM policy"
}

variable "cluster_node_ec2_policy_json" {
  type        = string
  description = "JSON of the EC2 assume role policy document"
}

variable "setup_bucket_name" {
  type = string
}

variable "setup_files_hash" {
  type = map(string)
}

variable "security_group_ids" {
  type = list(string)
}

variable "vpc_private_subnets" {
  type = list(string)
}

variable "image_family_prefix" {
  type    = string
  default = "e2b-orch-"
}

variable "cluster_size" {
  type    = number
  default = 1
}

variable "machine_type" {
  type    = string
  default = "m8i.4xlarge"
}

variable "node_pool_name" {
  type        = string
  description = "Nomad node pool name for client nodes"
}

variable "base_hugepages_percentage" {
  description = "The percentage of memory to use for preallocated hugepages."
  type        = number
  default     = 60
}

variable "nested_virtualization" {
  type    = bool
  default = true
}

variable "boot_disk_size_gb" {
  type        = number
  default     = 500
  description = "Root volume size in GB"
}

variable "consul_acl_token" {
  type = string
}

variable "consul_gossip_encryption_key" {
  type = string
}

variable "consul_dns_request_token" {
  type = string
}

variable "aws_ecr_account_repository_domain" {
  type = string
}

variable "fc_kernels_bucket_name" {
  type = string
}

variable "fc_versions_bucket_name" {
  type = string
}

variable "fc_env_pipeline_bucket_name" {
  type = string
}

variable "fc_env_pipeline_bucket_arn" {
  type = string
}

variable "fc_kernels_bucket_arn" {
  type = string
}

variable "fc_versions_bucket_arn" {
  type = string
}

variable "templates_bucket_arn" {
  type = string
}

variable "templates_build_cache_bucket_arn" {
  type = string
}

variable "custom_environments_repo_arn" {
  type = string
}

variable "scripts_path" {
  type        = string
  description = "Path to the directory containing startup scripts. Defaults to in-module scripts."
  default     = ""
}
