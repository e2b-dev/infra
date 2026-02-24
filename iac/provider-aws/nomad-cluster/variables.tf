variable "prefix" {
  type = string
}

variable "environment" {
  type = string
}

variable "cluster_tag_name" {
  type    = string
  default = "orch"
}

variable "aws_region" {
  type = string
}

variable "availability_zones" {
  type = list(string)
}

variable "vpc_id" {
  type = string
}

variable "public_subnet_ids" {
  type = list(string)
}

variable "private_subnet_ids" {
  type = list(string)
}

variable "cluster_sg_id" {
  type = string
}

variable "iam_instance_profile_name" {
  type = string
}

variable "ami_id" {
  type = string
}

# Server cluster
variable "server_cluster_size" {
  type = number
}

variable "server_instance_type" {
  type = string
}

# API cluster
variable "api_cluster_size" {
  type = number
}

variable "api_instance_type" {
  type = string
}

# Worker clusters
variable "build_clusters_config" {
  type = any
}

variable "client_clusters_config" {
  type = any
}

# Secrets
variable "consul_acl_token_secret" {
  type      = string
  sensitive = true
}

variable "nomad_acl_token_secret" {
  type      = string
  sensitive = true
}

# Storage buckets
variable "cluster_setup_bucket_name" {
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

variable "docker_contexts_bucket_name" {
  type = string
}

# Ports
variable "nomad_port" {
  type    = number
  default = 4646
}

variable "domain_name" {
  type = string
}

variable "api_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "ingress_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "docker_reverse_proxy_port" {
  type = object({
    name        = string
    port        = number
    health_path = string
  })
}

variable "client_proxy_port" {
  type = object({
    name = string
    port = number
  })
}

variable "client_proxy_health_port" {
  type = object({
    name = string
    port = number
    path = string
  })
}

# EFS
variable "efs_cache_enabled" {
  type    = bool
  default = false
}

variable "efs_dns_name" {
  type    = string
  default = ""
}

variable "efs_mount_path" {
  type    = string
  default = "/orchestrator/shared-store"
}

variable "tags" {
  type = map(string)
}
