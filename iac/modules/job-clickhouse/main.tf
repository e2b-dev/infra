locals {
  clickhouse_config = templatefile("${path.module}/configs/config.xml", {
    clickhouse_server_port  = var.clickhouse_port
    clickhouse_metrics_port = var.clickhouse_metrics_port

    server_secret = var.server_secret
    server_count  = var.server_count

    username = var.clickhouse_username
    password = var.clickhouse_password
  })

  clickhouse_users_config = templatefile("${path.module}/configs/users.xml", {
    username = var.clickhouse_username
    password = var.clickhouse_password
  })

  otel_agent_config = templatefile("${path.module}/configs/otel-agent.yaml", {
    clickhouse_metrics_port = var.clickhouse_metrics_port
    otel_exporter_endpoint  = var.otel_exporter_endpoint
    provider_name           = var.provider_name
  })

  backup_vars = {
    clickhouse_backup_version = var.clickhouse_backup_version

    server_count          = var.server_count
    clickhouse_username   = var.clickhouse_username
    clickhouse_password   = var.clickhouse_password
    clickhouse_port       = var.clickhouse_port
    job_constraint_prefix = var.job_constraint_prefix
    node_pool             = var.node_pool

    cloud_provider               = var.provider_name
    backup_bucket                = var.backup_bucket
    backup_folder                = var.backup_folder
    gcs_credentials_json_encoded = var.gcs_credentials_json_encoded
    aws_region                   = var.aws_region
  }
}

resource "nomad_job" "clickhouse" {
  count = var.server_count > 0 ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/clickhouse.hcl", {
    server_secret      = var.server_secret
    clickhouse_version = var.clickhouse_version

    cpu_count = var.cpu_count
    memory_mb = var.memory_mb

    username                = var.clickhouse_username
    clickhouse_metrics_port = var.clickhouse_metrics_port
    clickhouse_server_port  = var.clickhouse_port
    server_count            = var.server_count

    clickhouse_config       = local.clickhouse_config
    clickhouse_users_config = local.clickhouse_users_config
    otel_agent_config       = local.otel_agent_config

    job_constraint_prefix = var.job_constraint_prefix
    node_pool             = var.node_pool
  })
}

resource "nomad_job" "clickhouse_backup" {
  count = var.server_count > 0 ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/clickhouse-backup.hcl", local.backup_vars)
}

resource "nomad_job" "clickhouse_backup_restore" {
  count = var.server_count > 0 ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/clickhouse-backup-restore.hcl", local.backup_vars)
}

resource "nomad_job" "clickhouse_migrator" {
  count = var.server_count > 0 ? 1 : 0

  jobspec = templatefile("${path.module}/jobs/clickhouse-migrator.hcl", {
    image = var.clickhouse_migrator_image

    server_count          = var.server_count
    job_constraint_prefix = var.job_constraint_prefix
    node_pool             = var.node_pool

    clickhouse_username = var.clickhouse_username
    clickhouse_password = var.clickhouse_password
    clickhouse_port     = var.clickhouse_port
    clickhouse_database = var.clickhouse_database
  })
}
