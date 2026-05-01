moved {
  from = nomad_job.ingress
  to   = module.ingress.nomad_job.ingress
}

moved {
  from = nomad_job.client_proxy
  to   = module.client_proxy.nomad_job.client_proxy
}

moved {
  from = nomad_job.loki
  to   = module.loki.nomad_job.loki
}

moved {
  from = nomad_job.logs_collector
  to   = module.logs_collector.nomad_job.logs_collector
}

moved {
  from = nomad_job.clickhouse
  to   = module.clickhouse.nomad_job.clickhouse
}

moved {
  from = nomad_job.clickhouse_backup
  to   = module.clickhouse.nomad_job.clickhouse_backup
}

moved {
  from = nomad_job.clickhouse_backup_restore
  to   = module.clickhouse.nomad_job.clickhouse_backup_restore
}

moved {
  from = nomad_job.clickhouse_migrator
  to   = module.clickhouse.nomad_job.clickhouse_migrator
}

moved {
  from = nomad_job.otel_collector_nomad_server
  to   = module.otel_collector_nomad_server.nomad_job.otel_collector_nomad_server
}

moved {
  from = nomad_job.otel_collector
  to   = module.otel_collector.nomad_job.otel_collector
}

moved {
  from = module.orchestrator.nomad_job.orchestrator
  to   = module.orchestrator[0].nomad_job.orchestrator
}

moved {
  from = module.orchestrator.random_id.orchestrator_job
  to   = module.orchestrator[0].random_id.orchestrator_job
}

moved {
  from = module.orchestrator.nomad_variable.orchestrator_hash
  to   = module.orchestrator[0].nomad_variable.orchestrator_hash
}

moved {
  from = nomad_job.api
  to   = module.api.nomad_job.api
}

moved {
  from = nomad_job.redis[0]
  to   = module.redis[0].nomad_job.redis
}

moved {
  from = nomad_job.template_manager
  to   = module.template_manager.nomad_job.template_manager
}

moved {
  from = nomad_job.nomad_nodepool_apm[0]
  to   = module.template_manager_autoscaler[0].nomad_job.nomad_nodepool_apm
}
