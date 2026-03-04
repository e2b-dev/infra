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
  description = "Nomad node pool name for ClickHouse nodes"
}

variable "clickhouse_az" {
  type        = string
  description = "Availability zone for ClickHouse instances"
}

variable "clickhouse_subnet_id" {
  type        = string
  description = "Subnet ID for ClickHouse instances"
}

variable "clickhouse_backups_bucket_arn" {
  type        = string
  description = "ARN of the ClickHouse backups S3 bucket"
}

variable "job_constraint_prefix" {
  type        = string
  description = "Prefix for job constraint tags on instances"
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

variable "data_volume_size_gb" {
  type        = number
  default     = 100
  description = "Size of the EBS data volume for ClickHouse"
}

variable "scripts_path" {
  type        = string
  description = "Path to the directory containing startup scripts. Defaults to in-module scripts."
  default     = ""
}
