/**
 * Hetzner Init Module — Bootstrap
 *
 * Provisions:
 *   1. Hetzner Object Storage buckets (S3-compatible, EU)
 *   2. Cluster-bootstrap secrets (Nomad ACL, Consul ACL, gossip keys)
 *   3. Common cluster outputs consumed by NX.2.x sub-sprints
 */

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.51.0"
    }
    # AWS provider used purely for S3-compatible Hetzner Object Storage.
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.35.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.8"
    }
  }
}
