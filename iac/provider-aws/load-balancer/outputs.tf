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
