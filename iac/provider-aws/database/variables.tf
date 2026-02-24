variable "prefix" {
  type = string
}

variable "subnet_ids" {
  description = "Private subnet IDs for the database subnet group"
  type        = list(string)
}

variable "rds_sg_id" {
  description = "Security group ID for RDS"
  type        = string
}

variable "tags" {
  description = "Tags to apply to all resources"
  type        = map(string)
}
