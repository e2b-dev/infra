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
