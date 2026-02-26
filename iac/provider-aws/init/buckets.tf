# --- S3 Access Logging ---
# Central bucket for S3 server access logs (compliance requirement)

resource "aws_s3_bucket" "access_logs" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket = "${var.bucket_prefix}s3-access-logs"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "access_logs" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket = aws_s3_bucket.access_logs[0].id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "access_logs" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket = aws_s3_bucket.access_logs[0].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "access_logs" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket = aws_s3_bucket.access_logs[0].id

  rule {
    id     = "access-logs-lifecycle"
    status = "Enabled"

    transition {
      days          = 30
      storage_class = "STANDARD_IA"
    }

    expiration {
      days = 90
    }
  }
}

# Enable access logging on compliance-sensitive buckets

resource "aws_s3_bucket_logging" "fc_templates" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket        = aws_s3_bucket.fc_templates.id
  target_bucket = aws_s3_bucket.access_logs[0].id
  target_prefix = "fc-templates/"
}

resource "aws_s3_bucket_logging" "fc_kernels" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket        = aws_s3_bucket.fc_kernels.id
  target_bucket = aws_s3_bucket.access_logs[0].id
  target_prefix = "fc-kernels/"
}

resource "aws_s3_bucket_logging" "fc_env_pipeline" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket        = aws_s3_bucket.fc_env_pipeline.id
  target_bucket = aws_s3_bucket.access_logs[0].id
  target_prefix = "fc-env-pipeline/"
}

resource "aws_s3_bucket_logging" "clickhouse_backups" {
  count = var.enable_s3_access_logging ? 1 : 0

  bucket        = aws_s3_bucket.clickhouse_backups.id
  target_bucket = aws_s3_bucket.access_logs[0].id
  target_prefix = "clickhouse-backups/"
}

# --- Loki Storage ---
resource "aws_s3_bucket" "loki_storage" {
  bucket = "${var.bucket_prefix}loki-storage"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "loki_storage" {
  bucket = aws_s3_bucket.loki_storage.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "loki_storage" {
  bucket = aws_s3_bucket.loki_storage.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "loki_storage" {
  bucket = aws_s3_bucket.loki_storage.id

  rule {
    id     = "delete-after-8-days"
    status = "Enabled"

    expiration {
      days = 8
    }
  }
}

# --- Docker Contexts ---
resource "aws_s3_bucket" "envs_docker_context" {
  bucket = "${var.bucket_prefix}envs-docker-context"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "envs_docker_context" {
  bucket = aws_s3_bucket.envs_docker_context.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "envs_docker_context" {
  bucket = aws_s3_bucket.envs_docker_context.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

# --- Instance Setup ---
resource "aws_s3_bucket" "instance_setup" {
  bucket = "${var.bucket_prefix}instance-setup"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "instance_setup" {
  bucket = aws_s3_bucket.instance_setup.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "instance_setup" {
  bucket = aws_s3_bucket.instance_setup.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

# --- FC Kernels ---
resource "aws_s3_bucket" "fc_kernels" {
  bucket = "${var.bucket_prefix}fc-kernels"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "fc_kernels" {
  bucket = aws_s3_bucket.fc_kernels.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "fc_kernels" {
  bucket = aws_s3_bucket.fc_kernels.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

resource "aws_s3_bucket_versioning" "fc_kernels" {
  bucket = aws_s3_bucket.fc_kernels.id

  versioning_configuration {
    status = "Enabled"
  }
}

# --- FC Versions ---
resource "aws_s3_bucket" "fc_versions" {
  bucket = "${var.bucket_prefix}fc-versions"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "fc_versions" {
  bucket = aws_s3_bucket.fc_versions.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "fc_versions" {
  bucket = aws_s3_bucket.fc_versions.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

# --- FC Env Pipeline ---
resource "aws_s3_bucket" "fc_env_pipeline" {
  bucket = "${var.bucket_prefix}fc-env-pipeline"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "fc_env_pipeline" {
  bucket = aws_s3_bucket.fc_env_pipeline.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "fc_env_pipeline" {
  bucket = aws_s3_bucket.fc_env_pipeline.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

# --- ClickHouse Backups ---
resource "aws_s3_bucket" "clickhouse_backups" {
  bucket = "${var.bucket_prefix}clickhouse-backups"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "clickhouse_backups" {
  bucket = aws_s3_bucket.clickhouse_backups.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "clickhouse_backups" {
  bucket = aws_s3_bucket.clickhouse_backups.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "clickhouse_backups" {
  bucket = aws_s3_bucket.clickhouse_backups.id

  rule {
    id     = "delete-after-30-days"
    status = "Enabled"

    expiration {
      days = 30
    }

    transition {
      days          = 7
      storage_class = "STANDARD_IA"
    }
  }
}

# --- FC Templates ---
resource "aws_s3_bucket" "fc_templates" {
  bucket = var.template_bucket_name != "" ? var.template_bucket_name : "${var.bucket_prefix}fc-templates"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "fc_templates" {
  bucket = aws_s3_bucket.fc_templates.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "fc_templates" {
  bucket = aws_s3_bucket.fc_templates.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

resource "aws_s3_bucket_versioning" "fc_templates" {
  bucket = aws_s3_bucket.fc_templates.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_intelligent_tiering_configuration" "fc_templates" {
  bucket = aws_s3_bucket.fc_templates.id
  name   = "archive-tiering"

  tiering {
    access_tier = "ARCHIVE_ACCESS"
    days        = 90
  }

  tiering {
    access_tier = "DEEP_ARCHIVE_ACCESS"
    days        = 180
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "fc_templates" {
  bucket = aws_s3_bucket.fc_templates.id

  rule {
    id     = "abort-incomplete-multipart"
    status = "Enabled"

    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }
}

# --- FC Build Cache ---
resource "aws_s3_bucket" "fc_build_cache" {
  bucket = "${var.bucket_prefix}fc-build-cache"

  tags = var.tags
}

resource "aws_s3_bucket_public_access_block" "fc_build_cache" {
  bucket = aws_s3_bucket.fc_build_cache.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "fc_build_cache" {
  bucket = aws_s3_bucket.fc_build_cache.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.s3_kms_key_arn != "" ? "aws:kms" : "AES256"
      kms_master_key_id = var.s3_kms_key_arn != "" ? var.s3_kms_key_arn : null
    }
  }
}

resource "aws_s3_bucket_intelligent_tiering_configuration" "fc_build_cache" {
  bucket = aws_s3_bucket.fc_build_cache.id
  name   = "auto-tiering"

  tiering {
    access_tier = "ARCHIVE_ACCESS"
    days        = 90
  }
}
