locals {
  default_loki_config = templatefile(
    "${path.module}/configs/loki.yml", {
      provider_name            = var.provider_name
      loki_port                = var.loki_port
      bucket_name              = var.bucket_name
      aws_region               = var.aws_region
      loki_use_v13_schema_from = var.loki_use_v13_schema_from
    },
  )

  // Allow config files override for flexibility
  loki_config = var.loki_config_override != "" ? var.loki_config_override : local.default_loki_config
}

resource "nomad_job" "loki" {
  jobspec = templatefile("${path.module}/jobs/loki.hcl", {
    node_pool          = var.node_pool
    prevent_colocation = var.prevent_colocation

    loki_port  = var.loki_port
    loki_image = var.loki_image

    memory_mb = var.memory_mb
    cpu_count = var.cpu_count

    loki_config = local.loki_config
  })
}
