resource "aws_security_group" "instance" {
  vpc_id = var.vpc_id
  name   = "${var.prefix}${var.name}"

  ingress {
    from_port       = var.port
    to_port         = var.port
    protocol        = "tcp"
    security_groups = var.ingress_security_group_ids
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.prefix}${var.name}"
  }
}

resource "aws_elasticache_replication_group" "instance" {
  replication_group_id = "${var.prefix}${var.name}"
  description          = var.description

  node_type               = var.instance_type
  num_node_groups         = 1
  replicas_per_node_group = var.replica_size - 1

  automatic_failover_enabled = true
  transit_encryption_enabled = true
  transit_encryption_mode    = "required"

  engine               = "valkey"
  engine_version       = "8.2"
  parameter_group_name = "default.valkey8.cluster.on"

  port              = var.port
  subnet_group_name = var.subnet_group_name
  security_group_ids = [
    aws_security_group.instance.id
  ]

  tags = {
    Name = "${var.prefix}${var.name}"
  }
}

data "http" "ca_1" {
  url = "https://www.amazontrust.com/repository/AmazonRootCA1.pem"
}

data "http" "ca_2" {
  url = "https://www.amazontrust.com/repository/AmazonRootCA2.pem"
}

data "http" "ca_3" {
  url = "https://www.amazontrust.com/repository/AmazonRootCA3.pem"
}

data "http" "ca_4" {
  url = "https://www.amazontrust.com/repository/AmazonRootCA4.pem"
}

locals {
  redis_ca_pem_base64 = base64encode(
    join("\n", [data.http.ca_1.response_body, data.http.ca_2.response_body, data.http.ca_3.response_body, data.http.ca_4.response_body]),
  )
}
