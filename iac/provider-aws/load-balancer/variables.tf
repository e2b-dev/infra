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

variable "nlb_sg_id" {
  description = "Security group ID for the Network Load Balancer"
  type        = string
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

variable "client_proxy_health_port" {
  description = "Client proxy health check port configuration"
  type = object({
    port = number
    path = string
  })
  default = {
    port = 3001
    path = "/health"
  }
}

variable "eks_node_security_group_id" {
  description = "EKS node security group ID for target group health checks"
  type        = string
  default     = ""
}

variable "cloudflare_api_token_secret_arn" {
  type = string
}

variable "enable_waf_managed_rules" {
  description = "Enable AWS managed WAF rule groups (CommonRuleSet, KnownBadInputs, SQLi, IpReputation)"
  type        = bool
  default     = true
}

variable "session_deregistration_delay" {
  description = "Deregistration delay in seconds for NLB session target group (higher for long-lived WebSockets)"
  type        = number
  default     = 300
}

variable "tags" {
  type    = map(string)
  default = {}
}
