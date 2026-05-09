/**
 * Hetzner Object Storage Buckets — Bootstrap
 *
 * Hetzner Object Storage is S3-compatible (EU-sovereign, FSN1/NBG1/HEL1).
 * Provisioned via the AWS provider with custom endpoint.
 *
 * Bucket naming: {bucket_prefix}-{purpose}
 *   - {prefix}-tfstate         — Terraform state (when used by sub-sprints)
 *   - {prefix}-build-artifacts — Sandbox-build artifacts (rootfs, kernels, OCI tarballs)
 *   - {prefix}-cluster-logs    — LB access logs, audit logs
 *   - {prefix}-clickhouse-data — ClickHouse cold-storage tier
 *   - {prefix}-loki-chunks     — Loki log chunks
 *   - {prefix}-snapshots       — VM snapshot files
 *   - {prefix}-cookies         — Browser-cookie sync (Manus state-sync analog)
 *
 * Hetzner Object Storage notes:
 *   - Bucket-level encryption is server-side default (no extra config)
 *   - Versioning supported via aws_s3_bucket_versioning
 *   - Lifecycle rules supported via aws_s3_bucket_lifecycle_configuration
 *   - ACL "private" (Hetzner default behavior, no public unless explicitly opened)
 */

locals {
  bucket_purposes = [
    "build-artifacts",
    "cluster-logs",
    "clickhouse-data",
    "loki-chunks",
    "snapshots",
    "cookies",
    # NX.2.4 Storage-Extensions for Observability (NX.9 prep) + Manus-Pattern:
    "fc-kernels",        # Firecracker-Kernels (NX.4 produces these)
    "fc-versions",       # Firecracker-Binary-Versions
    "fc-env-pipeline",   # Template-Build artifacts (sandbox-runtime envs)
    "fc-busybox",        # Busybox-rootfs (minimal sandbox base)
    "mimir-blocks",      # Mimir TSDB blocks (NX.9 metrics cold-tier)
    "tempo-traces",      # Tempo traces (NX.9 distributed tracing)
    "grafana-snapshots", # Grafana dashboard snapshots
  ]
}

resource "aws_s3_bucket" "buckets" {
  for_each = toset(local.bucket_purposes)

  bucket        = "${var.bucket_prefix}-${each.key}"
  force_destroy = var.allow_force_destroy

  tags = merge(var.common_labels, {
    purpose = each.key
  })
}

# Versioning enabled for state and snapshots (point-in-time recovery).
resource "aws_s3_bucket_versioning" "versioned" {
  for_each = toset([
    "snapshots",
    "build-artifacts",
  ])

  bucket = aws_s3_bucket.buckets[each.key].id

  versioning_configuration {
    status = "Enabled"
  }
}

# Lifecycle: cluster-logs auto-expire after 90d, loki-chunks after 30d.
resource "aws_s3_bucket_lifecycle_configuration" "logs" {
  bucket = aws_s3_bucket.buckets["cluster-logs"].id

  rule {
    id     = "expire-old-logs"
    status = "Enabled"

    filter {}

    expiration {
      days = 90
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "loki_chunks" {
  bucket = aws_s3_bucket.buckets["loki-chunks"].id

  rule {
    id     = "expire-loki-chunks"
    status = "Enabled"

    filter {}

    expiration {
      days = 30
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "build_artifacts" {
  bucket = aws_s3_bucket.buckets["build-artifacts"].id

  rule {
    id     = "expire-old-non-current-versions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 30
    }
  }
}
