# EKS nodes security group (additional rules beyond EKS module managed SG)
resource "aws_security_group" "eks_nodes" {
  name_prefix = "${var.prefix}eks-nodes-"
  description = "Security group for EKS worker nodes"
  vpc_id      = aws_vpc.main.id

  # Application ports (orchestrator, client-proxy, API)
  ingress {
    description = "E2B application ports"
    from_port   = 3001
    to_port     = 3002
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Orchestrator ports"
    from_port   = 5007
    to_port     = 5009
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "API port"
    from_port   = 50001
    to_port     = 50001
    protocol    = "tcp"
    self        = true
  }

  # Kubernetes NodePort range
  ingress {
    description = "NodePort services"
    from_port   = 30000
    to_port     = 32767
    protocol    = "tcp"
    self        = true
  }

  # Metrics and monitoring
  ingress {
    description = "Metrics and ingress ports"
    from_port   = 8800
    to_port     = 8800
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "OTel collector"
    from_port   = 4317
    to_port     = 4318
    protocol    = "tcp"
    self        = true
  }

  # ClickHouse
  ingress {
    description = "ClickHouse"
    from_port   = 8123
    to_port     = 8123
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "ClickHouse native"
    from_port   = 9000
    to_port     = 9000
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "ClickHouse metrics"
    from_port   = 9363
    to_port     = 9363
    protocol    = "tcp"
    self        = true
  }

  # Docker reverse proxy
  ingress {
    description = "Docker reverse proxy"
    from_port   = 5000
    to_port     = 5000
    protocol    = "tcp"
    self        = true
  }

  # Loki
  ingress {
    description = "Loki"
    from_port   = 3100
    to_port     = 3100
    protocol    = "tcp"
    self        = true
  }

  # Redis (in-cluster)
  ingress {
    description = "Redis"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    self        = true
  }

  # VXLAN for pod-to-pod (VPC CNI)
  ingress {
    description = "VXLAN overlay"
    from_port   = 4789
    to_port     = 4789
    protocol    = "udp"
    self        = true
  }

  # K8s API server → nodes (kubelet)
  ingress {
    description = "Kubelet API"
    from_port   = 10250
    to_port     = 10250
    protocol    = "tcp"
    self        = true
  }

  # CoreDNS
  ingress {
    description = "CoreDNS TCP"
    from_port   = 53
    to_port     = 53
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "CoreDNS UDP"
    from_port   = 53
    to_port     = 53
    protocol    = "udp"
    self        = true
  }

  # Health checks from ALB — restricted to actual service ports
  ingress {
    description     = "ALB health check: API"
    from_port       = 50001
    to_port         = 50001
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  ingress {
    description     = "ALB health check: Ingress"
    from_port       = 8800
    to_port         = 8800
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  ingress {
    description     = "ALB health check: Docker reverse proxy"
    from_port       = 5000
    to_port         = 5000
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  ingress {
    description     = "ALB health check: Client proxy"
    from_port       = 3001
    to_port         = 3002
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  # SSH access — in production, restrict to within the cluster only
  ingress {
    description = "SSH"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    self        = var.environment != "dev"
    cidr_blocks = var.environment == "dev" ? [var.vpc_cidr] : []
  }

  # Intra-VPC: all traffic (K8s, RDS, ElastiCache, VPC endpoints)
  egress {
    description = "All traffic to VPC"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [var.vpc_cidr]
  }

  # HTTPS to internet (AWS APIs, container image pulls)
  egress {
    description = "HTTPS to internet"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # HTTP to internet (package managers, redirects)
  egress {
    description = "HTTP to internet"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # DNS resolution
  egress {
    description = "DNS UDP"
    from_port   = 53
    to_port     = 53
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "DNS TCP"
    from_port   = 53
    to_port     = 53
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Conditional: full egress when sandboxes need internet access
  dynamic "egress" {
    for_each = var.allow_sandbox_internet ? [1] : []
    content {
      description = "Full internet egress for sandboxes"
      from_port   = 0
      to_port     = 0
      protocol    = "-1"
      cidr_blocks = ["0.0.0.0/0"]
    }
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}eks-nodes"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ALB security group
resource "aws_security_group" "alb" {
  name_prefix = "${var.prefix}alb-"
  description = "Security group for Application Load Balancer"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "HTTP"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = var.restrict_egress_to_vpc ? [var.vpc_cidr] : ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}alb"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# NLB security group
resource "aws_security_group" "nlb" {
  name_prefix = "${var.prefix}nlb-"
  description = "Security group for Network Load Balancer"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "TLS from internet"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "To VPC targets"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = [var.vpc_cidr]
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}nlb"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# RDS security group
resource "aws_security_group" "rds" {
  name_prefix = "${var.prefix}rds-"
  description = "Security group for RDS PostgreSQL"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "PostgreSQL from cluster"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.eks_nodes.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = var.restrict_egress_to_vpc ? [var.vpc_cidr] : ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}rds"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# ElastiCache security group
resource "aws_security_group" "elasticache" {
  name_prefix = "${var.prefix}elasticache-"
  description = "Security group for ElastiCache Redis"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "Redis from cluster"
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [aws_security_group.eks_nodes.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = var.restrict_egress_to_vpc ? [var.vpc_cidr] : ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}elasticache"
  })

  lifecycle {
    create_before_destroy = true
  }
}

# EFS security group
resource "aws_security_group" "efs" {
  name_prefix = "${var.prefix}efs-"
  description = "Security group for EFS"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "NFS from cluster"
    from_port       = 2049
    to_port         = 2049
    protocol        = "tcp"
    security_groups = [aws_security_group.eks_nodes.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = var.restrict_egress_to_vpc ? [var.vpc_cidr] : ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}efs"
  })

  lifecycle {
    create_before_destroy = true
  }
}
