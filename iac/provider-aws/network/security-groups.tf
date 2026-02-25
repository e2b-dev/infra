# EKS nodes security group (additional rules beyond EKS module managed SG)
resource "aws_security_group" "eks_nodes" {
  name_prefix = "${var.prefix}eks-nodes-"
  description = "Security group for EKS worker nodes"
  vpc_id      = aws_vpc.main.id

  # Inter-node all traffic
  ingress {
    description = "All inter-node traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
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

  # Health checks from ALB
  ingress {
    description     = "Health checks from ALB"
    from_port       = 0
    to_port         = 65535
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

  # Allow all outbound
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
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
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}alb"
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
    cidr_blocks = ["0.0.0.0/0"]
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
    cidr_blocks = ["0.0.0.0/0"]
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
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}efs"
  })

  lifecycle {
    create_before_destroy = true
  }
}
