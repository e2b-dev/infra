output "alb_dns_name" {
  value = aws_lb.alb.dns_name
}

output "nlb_dns_name" {
  value = aws_lb.nlb.dns_name
}

output "alb_arn" {
  value = aws_lb.alb.arn
}

output "nlb_arn" {
  value = aws_lb.nlb.arn
}

output "api_target_group_arn" {
  value = aws_lb_target_group.api.arn
}

output "docker_reverse_proxy_target_group_arn" {
  value = aws_lb_target_group.docker_reverse_proxy.arn
}

output "ingress_target_group_arn" {
  value = aws_lb_target_group.ingress.arn
}

output "session_target_group_arn" {
  value = aws_lb_target_group.session.arn
}

output "alb_arn_suffix" {
  description = "ALB ARN suffix for CloudWatch metrics"
  value       = aws_lb.alb.arn_suffix
}
