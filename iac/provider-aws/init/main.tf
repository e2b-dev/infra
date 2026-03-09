data "aws_region" "current" {}

data "aws_elb_service_account" "current" {}

module "network" {
  source = "../modules/network"

  prefix                          = var.prefix
  vcp_availability_zones          = ["${var.region}a", "${var.region}b", "${var.region}c"]
  vpc_endpoint_ingress_subnet_ids = var.endpoint_ingress_subnet_ids
}

module "cloudflare" {
  source = "../modules/cloudflare"

  prefix = var.prefix
}
