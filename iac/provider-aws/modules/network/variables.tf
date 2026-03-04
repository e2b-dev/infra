variable "prefix" {
  type = string
}

variable "vpc_cidr" {
  type        = string
  default     = "10.0.0.0/16"
  description = "CIDR block for the VPC"
}

variable "vpc_public_subnets" {
  type        = list(string)
  default     = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
  description = "CIDRs for the public subnets in the VPC, at least three are required"
}

variable "vpc_private_subnets" {
  type        = list(string)
  default     = ["10.0.11.0/24", "10.0.12.0/24", "10.0.13.0/24", "10.0.14.0/24", "10.0.15.0/24", "10.0.16.0/24"]
  description = "CIDRs for the private subnets in the VPC, at least three are required"
}

variable "vpc_elasticache_subnets" {
  type    = list(string)
  default = ["10.0.21.0/24", "10.0.22.0/24", "10.0.23.0/24"]
}

variable "vcp_availability_zones" {
  type        = list(string)
  description = "List of availability zones to use for the VPC subnets"
}

variable "vpc_endpoint_ingress_subnet_ids" {
  type = list(string)
}

variable "use_instance_connect" {
  type        = bool
  default     = true
  description = "Whether to deploy AWS EC2 Instance Connect Endpoint for SSH access to EC2 instances"
}
