variable "prefix" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "public_subnet_ids" {
  type = list(string)
}

variable "alb_sg_id" {
  type = string
}

variable "domain_name" {
  type = string
}

variable "additional_domains" {
  type    = list(string)
  default = []
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

variable "eks_node_security_group_id" {
  description = "EKS node security group ID for target group health checks"
  type        = string
  default     = ""
}

variable "cloudflare_api_token_secret_arn" {
  type = string
}

variable "tags" {
  type    = map(string)
  default = {}
}
