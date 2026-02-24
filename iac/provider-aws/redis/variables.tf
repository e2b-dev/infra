variable "prefix" {
  type    = string
  default = "e2b-"
}

variable "subnet_ids" {
  description = "Private subnet IDs for the Redis subnet group"
  type        = list(string)
}

variable "redis_sg_id" {
  description = "Security group ID for ElastiCache"
  type        = string
}

variable "redis_node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.t3.medium"
}

variable "redis_shard_count" {
  description = "Number of shards in the Redis cluster"
  type        = number
  default     = 1
}

variable "redis_replica_count" {
  description = "Number of replicas per shard"
  type        = number
  default     = 1
}

variable "redis_cluster_url_secret_arn" {
  description = "ARN of the Secrets Manager secret to store the Redis connection URL"
  type        = string
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
}
