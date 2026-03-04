variable "prefix" {
  type = string
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

variable "target_group_arns" {
  type    = list(string)
  default = []
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
  default = "t3.xlarge"
}

variable "node_pool_name" {
  type        = string
  description = "Nomad node pool name for this pool"
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

variable "loki_bucket_arn" {
  type        = string
  description = "ARN of the Loki S3 bucket for IAM policy"
}

variable "scripts_path" {
  type        = string
  description = "Path to the directory containing startup scripts. Defaults to in-module scripts."
  default     = ""
}
