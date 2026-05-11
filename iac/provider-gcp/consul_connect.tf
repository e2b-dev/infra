locals {
  consul_service_intention_script = "${path.module}/scripts/consul-service-intention.sh"

  api_consul_connect_intention_sources = (var.api_consul_connect_enabled || var.consul_connect_enabled) ? toset([
    "client-proxy",
  ]) : toset([])

  clickhouse_consul_connect_intention_sources = var.consul_connect_enabled ? toset([
    "api-internal-grpc",
    "dashboard-api-connect",
  ]) : toset([])
}

resource "terraform_data" "api_consul_connect_intention" {
  for_each = local.api_consul_connect_intention_sources

  triggers_replace = {
    destination = "api-internal-grpc"
    gcp_project = var.gcp_project_id
    prefix      = var.prefix
    script_hash = filesha256(local.consul_service_intention_script)
    script_path = local.consul_service_intention_script
    source      = each.key
  }

  provisioner "local-exec" {
    interpreter = ["bash", "-c"]
    command     = "'${self.triggers_replace.script_path}' upsert '${self.triggers_replace.gcp_project}' '${self.triggers_replace.prefix}' '${self.triggers_replace.source}' '${self.triggers_replace.destination}'"
  }

  provisioner "local-exec" {
    when        = destroy
    interpreter = ["bash", "-c"]
    command     = "'${try(self.triggers_replace.script_path, "./scripts/consul-service-intention.sh")}' delete '${self.triggers_replace.gcp_project}' '${self.triggers_replace.prefix}' '${self.triggers_replace.source}' '${self.triggers_replace.destination}'"
  }
}

resource "terraform_data" "clickhouse_consul_connect_intention" {
  for_each = local.clickhouse_consul_connect_intention_sources

  triggers_replace = {
    destination = "clickhouse"
    gcp_project = var.gcp_project_id
    prefix      = var.prefix
    script_hash = filesha256(local.consul_service_intention_script)
    script_path = local.consul_service_intention_script
    source      = each.key
  }

  provisioner "local-exec" {
    interpreter = ["bash", "-c"]
    command     = "'${self.triggers_replace.script_path}' upsert '${self.triggers_replace.gcp_project}' '${self.triggers_replace.prefix}' '${self.triggers_replace.source}' '${self.triggers_replace.destination}'"
  }

  provisioner "local-exec" {
    when        = destroy
    interpreter = ["bash", "-c"]
    command     = "'${try(self.triggers_replace.script_path, "./scripts/consul-service-intention.sh")}' delete '${self.triggers_replace.gcp_project}' '${self.triggers_replace.prefix}' '${self.triggers_replace.source}' '${self.triggers_replace.destination}'"
  }
}
