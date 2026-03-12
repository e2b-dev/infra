resource "random_uuid" "nomad_acl_token" {}

resource "random_uuid" "consul_acl_token" {}

resource "random_uuid" "consul_dns_request_token" {}

resource "random_id" "consul_gossip_encryption_key" {
  byte_length = 32
}

resource "aws_secretsmanager_secret" "cluster" {
  name = "${var.prefix}cluster"
}

resource "aws_secretsmanager_secret_version" "cluster" {
  secret_id = aws_secretsmanager_secret.cluster.id
  secret_string = jsonencode({
    NOMAD_ACL_TOKEN              = random_uuid.nomad_acl_token.id,
    CONSUL_ACL_TOKEN             = random_uuid.consul_acl_token.id,
    CONSUL_DNS_REQUEST_TOKEN     = random_uuid.consul_dns_request_token.id,
    CONSUL_GOSSIP_ENCRYPTION_KEY = random_id.consul_gossip_encryption_key.b64_std,
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

data "aws_secretsmanager_secret_version" "cluster" {
  secret_id     = aws_secretsmanager_secret.cluster.id
  version_stage = "AWSCURRENT"
  depends_on    = [aws_secretsmanager_secret_version.cluster]
}

locals {
  cluster_raw = jsondecode(data.aws_secretsmanager_secret_version.cluster.secret_string)
}

output "cluster" {
  sensitive = true
  value = {
    nomad_acl_token              = local.cluster_raw["NOMAD_ACL_TOKEN"]
    consul_acl_token             = local.cluster_raw["CONSUL_ACL_TOKEN"]
    consul_dns_request_token     = local.cluster_raw["CONSUL_DNS_REQUEST_TOKEN"]
    consul_gossip_encryption_key = local.cluster_raw["CONSUL_GOSSIP_ENCRYPTION_KEY"]
  }
}
