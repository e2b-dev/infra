data "aws_region" "current" {}

data "aws_elb_service_account" "current" {}

module "network" {
  source = "../modules/network"

  prefix                          = var.prefix
  vpc_availability_zones          = ["${var.region}a", "${var.region}b", "${var.region}c"]
  vpc_endpoint_ingress_subnet_ids = var.endpoint_ingress_subnet_ids
}

module "cloudflare" {
  source = "../modules/cloudflare"
  count  = var.dns_provider == "cloudflare" ? 1 : 0

  prefix = var.prefix
}
