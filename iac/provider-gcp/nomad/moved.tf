moved {
  from = nomad_job.ingress
  to   = module.ingress.nomad_job.ingress
}

moved {
  from = nomad_job.client_proxy
  to   = module.client_proxy.nomad_job.client_proxy
}
