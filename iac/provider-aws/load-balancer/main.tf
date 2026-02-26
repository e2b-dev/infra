locals {
  parts        = split(".", var.domain_name)
  is_subdomain = length(local.parts) > 2
  subdomain    = local.is_subdomain ? join(".", slice(local.parts, 0, length(local.parts) - 2)) : ""
  root_domain  = local.is_subdomain ? join(".", slice(local.parts, length(local.parts) - 2, length(local.parts))) : var.domain_name

  domain_map = { for d in var.additional_domains : replace(d, ".", "-") => d }
}

# --- Cloudflare Provider ---
data "aws_secretsmanager_secret_version" "cloudflare_api_token" {
  secret_id = var.cloudflare_api_token_secret_arn
}

terraform {
  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.19.0"
    }
  }
}

provider "cloudflare" {
  api_token = data.aws_secretsmanager_secret_version.cloudflare_api_token.secret_string
}

# --- Application Load Balancer (HTTP routing: API, Docker, Ingress) ---
resource "aws_lb" "alb" {
  name               = "${var.prefix}alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [var.alb_sg_id]
  subnets            = var.public_subnet_ids

  enable_deletion_protection = true

  tags = merge(var.tags, {
    Name = "${var.prefix}alb"
  })
}

# --- Network Load Balancer (WebSocket session traffic, unlimited idle timeout) ---
resource "aws_lb" "nlb" {
  name               = "${var.prefix}nlb"
  internal           = false
  load_balancer_type = "network"
  security_groups    = [var.nlb_sg_id]
  subnets            = var.public_subnet_ids

  enable_deletion_protection = true

  tags = merge(var.tags, {
    Name = "${var.prefix}nlb"
  })
}

# --- ALB Target Groups ---
resource "aws_lb_target_group" "api" {
  name        = "${var.prefix}api"
  port        = var.api_port.port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    path                = var.api_port.health_path
    port                = var.api_port.port
    protocol            = "HTTP"
    healthy_threshold   = 2
    unhealthy_threshold = 2
    timeout             = 3
    interval            = 5
  }

  deregistration_delay = 30

  tags = var.tags
}

resource "aws_lb_target_group" "docker_reverse_proxy" {
  name        = "${var.prefix}docker"
  port        = var.docker_reverse_proxy_port.port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    path                = var.docker_reverse_proxy_port.health_path
    port                = var.docker_reverse_proxy_port.port
    protocol            = "HTTP"
    healthy_threshold   = 2
    unhealthy_threshold = 2
    timeout             = 3
    interval            = 5
  }

  deregistration_delay = 30

  tags = var.tags
}

resource "aws_lb_target_group" "ingress" {
  name        = "${var.prefix}ingress"
  port        = var.ingress_port.port
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    path                = var.ingress_port.health_path
    port                = var.ingress_port.port
    protocol            = "HTTP"
    healthy_threshold   = 2
    unhealthy_threshold = 2
    timeout             = 3
    interval            = 5
  }

  deregistration_delay = 30

  tags = var.tags
}

# --- NLB Target Group (TCP for WebSocket sessions) ---
resource "aws_lb_target_group" "session" {
  name        = "${var.prefix}session"
  port        = var.client_proxy_port.port
  protocol    = "TCP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    protocol            = "HTTP"
    port                = var.client_proxy_health_port.port
    path                = var.client_proxy_health_port.path
    healthy_threshold   = 2
    unhealthy_threshold = 2
    interval            = 10
  }

  deregistration_delay = var.session_deregistration_delay

  tags = var.tags
}

# --- ALB Listeners ---
resource "aws_lb_listener" "alb_https" {
  load_balancer_arn = aws_lb.alb.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = aws_acm_certificate_validation.main.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.ingress.arn
  }
}

resource "aws_lb_listener" "alb_http_redirect" {
  load_balancer_arn = aws_lb.alb.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

# --- ALB Listener Rules (host-based routing) ---
resource "aws_lb_listener_rule" "api" {
  listener_arn = aws_lb_listener.alb_https.arn
  priority     = 10

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }

  condition {
    host_header {
      values = concat(
        ["api.${var.domain_name}"],
        [for d in var.additional_domains : "api.${d}"]
      )
    }
  }
}

resource "aws_lb_listener_rule" "docker_reverse_proxy" {
  listener_arn = aws_lb_listener.alb_https.arn
  priority     = 20

  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.docker_reverse_proxy.arn
  }

  condition {
    host_header {
      values = concat(
        ["docker.${var.domain_name}"],
        [for d in var.additional_domains : "docker.${d}"]
      )
    }
  }
}

# --- NLB Listener (TLS for WebSocket session traffic) ---
resource "aws_lb_listener" "nlb_tls" {
  load_balancer_arn = aws_lb.nlb.arn
  port              = 443
  protocol          = "TLS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = aws_acm_certificate_validation.main.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.session.arn
  }
}

# --- Target Group Outputs ---
# Target group ARNs are exported for EKS TargetGroupBinding resources
# or AWS Load Balancer Controller to manage registration automatically.
