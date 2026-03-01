terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.33"
    }

    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "4.48.0"
    }

    nomad = {
      source  = "hashicorp/nomad"
      version = "2.1.0"
    }

    random = {
      source  = "hashicorp/random"
      version = "~> 3.1"
    }
  }

  required_version = ">= 1.0"

  backend "s3" {
    key = "terraform/orchestration/state"
  }
}

provider "cloudflare" {
  api_token = module.init.cloudflare.token
}

provider "aws" {}

data "aws_region" "current" {}

data "aws_caller_identity" "current" {}

data "aws_elb_service_account" "current" {}

module "init" {
  source = "./init"

  prefix        = var.prefix
  bucket_prefix = var.bucket_prefix

  region                      = data.aws_region.current.name
  endpoint_ingress_subnet_ids = [
    aws_security_group.cluster_node.id
  ]

  allow_force_destroy = var.allow_force_destroy
}

