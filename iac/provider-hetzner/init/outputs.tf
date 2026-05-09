/**
 * Hetzner Init Module — Outputs
 *
 * These outputs are consumed by the parent module (provider-hetzner/main.tf)
 * and forwarded to NX.2.x sub-sprints (network, compute, nomad-cluster, etc.).
 */

# ─────────────────────────── Buckets ───────────────────────────

output "bucket_ids" {
  description = "Map of bucket purpose → Object Storage bucket name."
  value = {
    for purpose, bucket in aws_s3_bucket.buckets :
    purpose => bucket.id
  }
}

output "bucket_arns" {
  description = "Map of bucket purpose → ARN (S3-compatible)."
  value = {
    for purpose, bucket in aws_s3_bucket.buckets :
    purpose => bucket.arn
  }
}

# ─────────────────────────── Cluster Bootstrap Secrets ───────────────────────────

output "cluster" {
  description = "Cluster bootstrap secrets and shared metadata."
  sensitive   = true
  value = {
    nomad_acl_bootstrap_token  = random_uuid.nomad_acl_bootstrap.result
    nomad_acl_token            = random_uuid.nomad_acl_token.result
    consul_acl_bootstrap_token = random_uuid.consul_acl_bootstrap.result
    consul_acl_token           = random_uuid.consul_acl_token.result
    nomad_gossip_key           = base64encode(random_id.nomad_gossip_key.b64_std)
    consul_gossip_key          = base64encode(random_id.consul_gossip_key.b64_std)

    secrets_bundle_bucket = aws_s3_bucket.buckets["cluster-logs"].id
    secrets_bundle_key    = aws_s3_object.secrets_bundle.key
  }
}

# ─────────────────────────── Common Cluster Metadata ───────────────────────────

output "domain_name" {
  description = "Primary domain or subdomain for this deployment."
  value       = var.domain_name
}

output "domain_root" {
  description = "Root domain (e.g. helix12.eu)."
  value       = var.domain_root
}

output "object_store_url" {
  description = "Hetzner Object Storage endpoint URL."
  value       = var.object_store_url
}

output "ssh_key_ids" {
  description = "Hetzner Cloud SSH key IDs to attach to all servers."
  value       = var.ssh_key_ids
}

output "common_labels" {
  description = "Common labels to apply to all downstream resources."
  value       = var.common_labels
}
