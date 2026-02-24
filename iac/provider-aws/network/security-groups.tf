# Nomad cluster security group
resource "aws_security_group" "nomad_cluster" {
  name_prefix = "${var.prefix}nomad-cluster-"
  description = "Security group for Nomad/Consul cluster nodes"
  vpc_id      = aws_vpc.main.id

  # Inter-node communication (Nomad)
  ingress {
    description = "Nomad HTTP API"
    from_port   = 4646
    to_port     = 4646
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Nomad RPC"
    from_port   = 4647
    to_port     = 4647
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Nomad Serf WAN"
    from_port   = 4648
    to_port     = 4648
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Nomad Serf WAN UDP"
    from_port   = 4648
    to_port     = 4648
    protocol    = "udp"
    self        = true
  }

  # Inter-node communication (Consul)
  ingress {
    description = "Consul HTTP API"
    from_port   = 8500
    to_port     = 8500
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Consul DNS"
    from_port   = 8600
    to_port     = 8600
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Consul DNS UDP"
    from_port   = 8600
    to_port     = 8600
    protocol    = "udp"
    self        = true
  }

  ingress {
    description = "Consul RPC"
    from_port   = 8300
    to_port     = 8300
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Consul Serf LAN"
    from_port   = 8301
    to_port     = 8302
    protocol    = "tcp"
    self        = true
  }

  ingress {
    description = "Consul Serf LAN UDP"
    from_port   = 8301
    to_port     = 8302
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
    Name = "${var.prefix}nomad-cluster"
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
    security_groups = [aws_security_group.nomad_cluster.id]
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
    security_groups = [aws_security_group.nomad_cluster.id]
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
    security_groups = [aws_security_group.nomad_cluster.id]
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
