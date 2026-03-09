variable "prefix" {
  type = string
}

variable "aws_account_id" {
  type = string
}

variable "aws_region" {
  type = string
}

variable "vpc_private_subnets" {
  type = list(string)
}

variable "vpc_public_subnets" {
  type = list(string)
}

variable "nomad_acl_token_secret" {
  type = string
}

variable "consul_acl_token_secret" {
  type = string
}

variable "consul_gossip_encryption_key" {
  type = string
}

variable "consul_dns_request_token_secret" {
  type = string
}

// ---
// Control Server
// ---

variable "control_server_security_group_ids" {
  type = list(string)
}

variable "control_server_target_group_arns" {
  type = list(string)
}

variable "control_server_image_family_prefix" {
  type = string
}

variable "control_server_cluster_size" {
  type = number
}

variable "control_server_machine_type" {
  type = string
}

// ---
// API Nodes
// ---

variable "api_node_pool_name" {
  type = string
}

variable "api_image_family_prefix" {
  type = string
}

variable "api_cluster_size" {
  type = number
}

variable "api_machine_type" {
  type = string
}

variable "api_target_group_arns" {
  type = list(string)
}

variable "api_security_group_ids" {
  type = list(string)
}

// ---
// Clickhouse
// ---

variable "clickhouse_node_pool_name" {
  type = string
}

variable "clickhouse_image_family_prefix" {
  type = string
}

variable "clickhouse_cluster_size" {
  type = number
}

variable "clickhouse_az" {
  type = string
}

variable "clickhouse_job_constraint_prefix" {
  type = string
}

variable "clickhouse_subnet_id" {
  type = string
}

variable "clickhouse_machine_type" {
  type = string
}

variable "clickhouse_security_group_ids" {
  type = list(string)
}

// ---
// Client
// ---
variable "client_node_pool_name" {
  type = string
}

variable "client_base_hugepages_percentage" {
  description = "The percentage of memory to use for preallocated hugepages."
  type        = number
  default     = 60
}

variable "client_image_family_prefix" {
  type = string
}

variable "client_cluster_size" {
  type = number
}

variable "client_machine_type" {
  type = string
}

variable "client_server_nested_virtualization" {
  type = bool
}

variable "client_security_group_ids" {
  type = list(string)
}

// ---
// Build (Template Manager)
// ---
variable "build_node_pool_name" {
  type = string
}

variable "build_image_family_prefix" {
  type    = string
  default = "e2b-orch-"
}

variable "build_cluster_size" {
  type    = number
  default = 1
}

variable "build_machine_type" {
  type    = string
  default = "m8i.2xlarge"
}

variable "build_server_nested_virtualization" {
  type = bool
}

variable "build_security_group_ids" {
  type = list(string)
}

// ---
// Buckets and repositories
// ---

variable "setup_bucket_name" {
  type = string
}

variable "fc_env_pipeline_bucket_name" {
  type = string
}

variable "fc_kernels_bucket_name" {
  type = string
}

variable "fc_versions_bucket_name" {
  type = string
}

variable "templates_build_cache_bucket_name" {
  type = string
}

variable "templates_bucket_name" {
  type = string
}

variable "loki_bucket_name" {
  type = string
}

variable "clickhouse_backups_bucket_name" {
  type = string
}

variable "custom_environments_repository_name" {
  type = string
}
