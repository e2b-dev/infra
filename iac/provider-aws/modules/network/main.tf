module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.5.3"

  name = "${var.prefix}vpc"
  cidr = var.vpc_cidr

  azs                 = var.vcp_availability_zones
  public_subnets      = var.vpc_public_subnets
  private_subnets     = var.vpc_private_subnets
  elasticache_subnets = var.vpc_elasticache_subnets

  elasticache_subnet_assign_ipv6_address_on_creation                = false
  elasticache_subnet_enable_resource_name_dns_aaaa_record_on_launch = false
  elasticache_subnet_enable_dns64                                   = false

  create_database_subnet_group           = false
  create_database_subnet_route_table     = false
  create_database_internet_gateway_route = false

  manage_default_security_group = false
  manage_default_route_table    = false
  manage_default_network_acl    = false

  enable_dns_support   = true
  enable_dns_hostnames = true

  single_nat_gateway = true // share NAT Gateway for all private subnets, otherwise it will create NAT per AZ
  enable_nat_gateway = true

  map_public_ip_on_launch = false
}

data "aws_subnet" "default_private" {
  for_each   = toset(var.vpc_private_subnets)
  vpc_id     = module.vpc.vpc_id
  cidr_block = each.value
  depends_on = [
    module.vpc
  ]
}

data "aws_subnet" "default_public" {
  for_each   = toset(var.vpc_public_subnets)
  vpc_id     = module.vpc.vpc_id
  cidr_block = each.value
  depends_on = [
    module.vpc
  ]
}

locals {
  default_private_subnet_ids = [
    for subnet in data.aws_subnet.default_private : subnet.id
  ]

  default_public_subnet_ids = [
    for subnet in data.aws_subnet.default_public : subnet.id
  ]
}

resource "aws_security_group" "vpc_endpoint" {
  name        = "${var.prefix}vpc-endpoint"
  description = "Allow traffic to AWS VPC endpoints"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "Allow HTTPS traffic to AWS VPC endpoints"
    from_port       = 443
    to_port         = 443
    protocol        = "TCP"
    security_groups = var.vpc_endpoint_ingress_subnet_ids
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.prefix}vpc-endpoint"
  }
}

module "vpc_endpoints" {
  source  = "terraform-aws-modules/vpc/aws//modules/vpc-endpoints"
  version = "5.5.3"

  vpc_id = module.vpc.vpc_id
  security_group_ids = [
    aws_security_group.vpc_endpoint.id
  ]

  endpoints = {
    s3 = {
      service         = "s3"
      service_type    = "Gateway"
      route_table_ids = module.vpc.private_route_table_ids
      tags = {
        Name = "${var.prefix}s3-vpc-endpoint"
      }
    },

    secrets_manager = {
      service      = "secretsmanager"
      service_type = "Interface" // gateway endpoint nots supported
      subnet_ids = [
        local.default_private_subnet_ids[0],
        local.default_private_subnet_ids[1],
        local.default_private_subnet_ids[2],
      ]
      tags = {
        Name = "${var.prefix}secrets-manager-vpc-endpoint"
      }
    },

    ec2 = {
      service      = "ec2"
      service_type = "Interface"
      subnet_ids = [
        local.default_private_subnet_ids[0],
        local.default_private_subnet_ids[1],
        local.default_private_subnet_ids[2],
      ],
      tags = {
        Name = "${var.prefix}ec2-vpc-endpoint"
      }
    }
  }
}

resource "aws_ec2_instance_connect_endpoint" "connect" {
  // Deploy only if enabled
  count = var.use_instance_connect ? 1 : 0

  preserve_client_ip = false
  subnet_id          = module.vpc.private_subnets[0]
  security_group_ids = [
    aws_security_group.connect_endpoint[0].id
  ]

  tags = {
    Name = "${var.prefix}-instance-connect-vpc-endpoint"
  }
}

// ingress rule for SSH access is not needed there because AWS Instance Connect
resource "aws_security_group" "connect_endpoint" {
  // Deploy only if enabled
  count = var.use_instance_connect ? 1 : 0

  name        = "${var.prefix}instance-connect-endpoint"
  description = "Allow EC2 Instance SSH Connect Access"
  vpc_id      = module.vpc.vpc_id

  egress {
    from_port = 22
    to_port   = 22
    protocol  = "tcp"
    cidr_blocks = [
      module.vpc.vpc_cidr_block
    ]
  }

  tags = {
    Name = "${var.prefix}instance-connect-endpoint"
  }
}
