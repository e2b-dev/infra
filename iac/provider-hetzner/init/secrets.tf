/**
 * Cluster-Bootstrap Secrets
 *
 * Generates ACL tokens and gossip keys consumed by the Nomad/Consul cluster.
 * Stored as objects in the cluster-logs bucket (encrypted at rest by Hetzner).
 *
 * NX.2.x sub-sprints retrieve these via output.cluster.{nomad_acl_token, consul_acl_token, ...}.
 *
 * Note: For production, secrets should be migrated to HashiCorp Vault on the
 * Operator host (already provisioned). This module provides the bootstrap path
 * for first-deployment when Vault may not yet be reachable from the Hetzner cluster.
 */

# ─────────────────────────── Nomad ACL Bootstrap Token ───────────────────────────

resource "random_uuid" "nomad_acl_bootstrap" {}

resource "random_uuid" "nomad_acl_token" {}

# ─────────────────────────── Consul ACL Bootstrap Token ───────────────────────────

resource "random_uuid" "consul_acl_bootstrap" {}

resource "random_uuid" "consul_acl_token" {}

# ─────────────────────────── Gossip Keys (32-byte AES) ───────────────────────────

resource "random_id" "nomad_gossip_key" {
  byte_length = 32
}

resource "random_id" "consul_gossip_key" {
  byte_length = 32
}

# ─────────────────────────── Object-Storage-Persisted Secrets ───────────────────────────
# Stored encrypted-at-rest in Hetzner Object Storage cluster-logs bucket.
# Consumed by Cloud-Init scripts on Nomad/Consul nodes during first boot.

resource "aws_s3_object" "secrets_bundle" {
  bucket = aws_s3_bucket.buckets["cluster-logs"].id
  key    = "bootstrap/secrets.json"

  content = jsonencode({
    nomad_acl_bootstrap_token  = random_uuid.nomad_acl_bootstrap.result
    nomad_acl_token            = random_uuid.nomad_acl_token.result
    consul_acl_bootstrap_token = random_uuid.consul_acl_bootstrap.result
    consul_acl_token           = random_uuid.consul_acl_token.result
    nomad_gossip_key           = base64encode(random_id.nomad_gossip_key.b64_std)
    consul_gossip_key          = base64encode(random_id.consul_gossip_key.b64_std)
  })

  content_type = "application/json"

  tags = merge(var.common_labels, {
    purpose = "cluster-bootstrap-secrets"
  })
}
