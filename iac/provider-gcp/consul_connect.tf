locals {
  api_consul_connect_intention_sources = var.api_consul_connect_enabled ? toset([
    "client-proxy",
  ]) : toset([])
}

resource "terraform_data" "api_consul_connect_intention" {
  for_each = local.api_consul_connect_intention_sources

  triggers_replace = {
    destination = "api-internal-grpc"
    gcp_project = var.gcp_project_id
    prefix      = var.prefix
    source      = each.key
  }

  provisioner "local-exec" {
    interpreter = ["bash", "-c"]
    command     = <<-EOT
      set -euo pipefail

      server=$(gcloud compute instances list \
        --project='${self.triggers_replace.gcp_project}' \
        --filter='name~^${self.triggers_replace.prefix}orch-server-' \
        --format='value(name,zone)' \
        | head -n1)

      if [[ -z "$${server}" ]]; then
        echo "No Consul server instance found for prefix ${self.triggers_replace.prefix}" >&2
        exit 1
      fi

      read -r name zone <<<"$${server}"

      gcloud compute ssh "$${name}" \
        --zone "$${zone}" \
        --project='${self.triggers_replace.gcp_project}' \
        --command='consul intention create -allow ${self.triggers_replace.source} ${self.triggers_replace.destination} || consul intention get ${self.triggers_replace.source} ${self.triggers_replace.destination} >/dev/null'
    EOT
  }

  provisioner "local-exec" {
    when        = destroy
    interpreter = ["bash", "-c"]
    command     = <<-EOT
      set -euo pipefail

      server=$(gcloud compute instances list \
        --project='${self.triggers_replace.gcp_project}' \
        --filter='name~^${self.triggers_replace.prefix}orch-server-' \
        --format='value(name,zone)' \
        | head -n1)

      if [[ -z "$${server}" ]]; then
        exit 0
      fi

      read -r name zone <<<"$${server}"

      gcloud compute ssh "$${name}" \
        --zone "$${zone}" \
        --project='${self.triggers_replace.gcp_project}' \
        --command='consul intention delete ${self.triggers_replace.source} ${self.triggers_replace.destination} || true'
    EOT
  }
}
