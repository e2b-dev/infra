locals {
  server_ips       = [for s in var.servers : s.host]
  bootstrap_expect = length(var.servers)
  client_map       = { for c in var.clients : c.host => c }
}

resource "null_resource" "servers" {
  for_each = { for s in var.servers : s.host => s }

  triggers = {
    docker_http_proxy  = var.docker_http_proxy
    docker_https_proxy = var.docker_https_proxy
    docker_no_proxy    = var.docker_no_proxy
    docker_image_prefix = var.docker_image_prefix
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "file" {
    content = jsonencode(merge(
      {
        datacenter       = var.datacenter,
        data_dir         = "/var/lib/consul",
        server           = true,
        bootstrap_expect = local.bootstrap_expect,
        bind_addr        = each.value.host,
        client_addr      = "0.0.0.0",
        retry_join       = local.server_ips,
        recursors        = ["8.8.8.8", "1.1.1.1"]
      },
      length(var.consul_acl_token) > 0 ? {
        acl = {
          enabled                  = true,
          default_policy           = "deny",
          enable_token_persistence = true,
          tokens                   = { default = var.consul_acl_token }
        }
      } : {}
    ))
    destination = "/tmp/consul.json"
  }

  provisioner "file" {
    content = jsonencode(merge(
      {
        datacenter = var.datacenter,
        data_dir   = "/var/lib/nomad",
        bind_addr  = "0.0.0.0",
        server     = { enabled = true, bootstrap_expect = local.bootstrap_expect },
        consul     = { address = "127.0.0.1:8500" }
      },
      contains(keys(local.client_map), each.value.host) ? {
        client = {
          enabled   = true,
          node_pool = local.client_map[each.value.host].node_pool,
          servers   = [for s in local.server_ips : "${s}:4647"]
        }
      } : {}
    ))
    destination = "/tmp/nomad.json"
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if ! sudo -n true 2>/dev/null; then echo 'Passwordless sudo required for provisioning. Configure /etc/sudoers.d/$(whoami) or connect as root.'; exit 1; fi",
      "export DEBIAN_FRONTEND=noninteractive",
      "sudo apt-get update -y",
      "sudo apt-get install -y curl",
      "USR=\"$(whoami)\"; echo \"$USR ALL=(ALL) NOPASSWD:ALL\" | sudo tee /etc/sudoers.d/$USR >/dev/null; sudo chmod 0440 /etc/sudoers.d/$USR; sudo usermod -aG sudo $USR",
      "if ! command -v docker >/dev/null 2>&1; then (curl -fsSL https://get.docker.com | sh) || (sudo apt-get update -y && sudo apt-get install -y docker.io); fi",
      "PREFIX=\"${var.docker_image_prefix}\"",
      "REG=$(echo \"$PREFIX\" | sed -E 's|^(https?://)?([^/]+).*|\\2|')",
      "if [ -n \"$REG\" ]; then",
      "  if echo \"$PREFIX\" | grep -qE '^http://'; then INSECURE=1;",
      "  elif echo \"$PREFIX\" | grep -qE '^[^/]+:[0-9]+' && ! echo \"$PREFIX\" | grep -qE '^https://'; then INSECURE=1; else INSECURE=0; fi",
      "  if [ \"$INSECURE\" = 1 ]; then sudo mkdir -p /etc/docker; printf '{\"insecure-registries\":[\"%s\"]}\n' \"$REG\" | sudo tee /etc/docker/daemon.json >/dev/null; sudo systemctl restart docker; fi",
      "fi",
      "HTTP_PROXY=\"${var.docker_http_proxy}\"",
      "HTTPS_PROXY=\"${var.docker_https_proxy}\"",
      "NO_PROXY=\"${var.docker_no_proxy}\"",
      "if [ -n \"$HTTP_PROXY$HTTPS_PROXY$NO_PROXY\" ]; then",
      "  sudo mkdir -p /etc/systemd/system/docker.service.d",
      "  printf '[Service]\n' | sudo tee /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTP_PROXY\" ] && echo \"Environment=\"\"HTTP_PROXY=$HTTP_PROXY\"\"\" | sudo tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTPS_PROXY\" ] && echo \"Environment=\"\"HTTPS_PROXY=$HTTPS_PROXY\"\"\" | sudo tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$NO_PROXY\" ] && echo \"Environment=\"\"NO_PROXY=$NO_PROXY\"\"\" | sudo tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  sudo systemctl daemon-reload",
      "  sudo systemctl restart docker",
      "fi",
      "sudo apt-get install -y unzip gnupg lsb-release",
      "curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo apt-key add -",
      "CODENAME=$(lsb_release -cs); echo \"deb [arch=amd64] https://apt.releases.hashicorp.com $CODENAME main\" | sudo tee /etc/apt/sources.list.d/hashicorp.list >/dev/null",
      "sudo apt-get update -y",
      "sudo apt-get install -y consul nomad",
      "sudo mkdir -p /etc/consul.d /etc/nomad.d",
      "sudo mkdir -p /var/lib/consul /var/lib/nomad",
      "sudo chown -R consul:consul /var/lib/consul /etc/consul.d",
      "sudo chown -R nomad:nomad /var/lib/nomad /etc/nomad.d",
      "sudo mv /tmp/consul.json /etc/consul.d/consul.json",
      "sudo mv /tmp/nomad.json /etc/nomad.d/nomad.json",
      "sudo find /etc/nomad.d -type f ! -name 'nomad.json' -delete || true",
      "sudo systemctl enable consul",
      "sudo systemctl restart consul",
      "sudo systemctl enable nomad",
      "sudo systemctl restart nomad",
      "printf 'node_pool \"api\" {\n  description = \"Nodes for api.\"\n}\n' | sudo tee /tmp/api_node_pool.hcl >/dev/null",
      "printf 'node_pool \"build\" {\n  description = \"Nodes for template builds.\"\n}\n' | sudo tee /tmp/build_node_pool.hcl >/dev/null",
      "TOKEN=\"${var.nomad_acl_token}\"; if [ -n \"$TOKEN\" ]; then sudo nomad node pool apply -token \"$TOKEN\" /tmp/api_node_pool.hcl; else sudo nomad node pool apply /tmp/api_node_pool.hcl; fi",
      "TOKEN=\"${var.nomad_acl_token}\"; if [ -n \"$TOKEN\" ]; then sudo nomad node pool apply -token \"$TOKEN\" /tmp/build_node_pool.hcl; else sudo nomad node pool apply /tmp/build_node_pool.hcl; fi",
      "sudo mkdir -p /clickhouse/data",
      "sudo mkdir -p /etc/systemd/resolved.conf.d/",
      "echo '[Resolve]\nDNS=127.0.0.1:8600\nDNSSEC=false' | sudo tee /etc/systemd/resolved.conf.d/consul.conf > /dev/null",
      "sudo systemctl restart systemd-resolved"
    ]
  }
}

resource "null_resource" "clients" {
  for_each = { for c in var.clients : c.host => c }

  triggers = {
    docker_http_proxy  = var.docker_http_proxy
    docker_https_proxy = var.docker_https_proxy
    docker_no_proxy    = var.docker_no_proxy
    docker_image_prefix = var.docker_image_prefix
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "file" {
    content = jsonencode(merge(
      {
        datacenter  = var.datacenter,
        data_dir    = "/var/lib/consul",
        server      = false,
        bind_addr   = each.value.host,
        client_addr = "0.0.0.0",
        retry_join  = local.server_ips,
        recursors   = ["8.8.8.8", "1.1.1.1"]
      },
      length(var.consul_acl_token) > 0 ? {
        acl = {
          enabled                  = true,
          default_policy           = "deny",
          enable_token_persistence = true,
          tokens                   = { default = var.consul_acl_token }
        }
      } : {}
    ))
    destination = "/tmp/consul.json"
  }

  provisioner "file" {
    content     = jsonencode({ datacenter = var.datacenter, data_dir = "/var/lib/nomad", bind_addr = "0.0.0.0", client = { enabled = true, node_pool = each.value.node_pool, servers = [for s in local.server_ips : "${s}:4647"] }, consul = { address = "127.0.0.1:8500" } })
    destination = "/tmp/nomad.json"
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if ! sudo -n true 2>/dev/null; then echo 'Passwordless sudo required for provisioning. Configure /etc/sudoers.d/$(whoami) or connect as root.'; exit 1; fi",
      "export DEBIAN_FRONTEND=noninteractive",
      "sudo apt-get update -y",
      "sudo apt-get install -y curl",
      "USR=\"$(whoami)\"; echo \"$USR ALL=(ALL) NOPASSWD:ALL\" | sudo tee /etc/sudoers.d/$USR >/dev/null; sudo chmod 0440 /etc/sudoers.d/$USR; sudo usermod -aG sudo $USR",
      "if ! command -v docker >/dev/null 2>&1; then (curl -fsSL https://get.docker.com | sh) || (sudo apt-get update -y && sudo apt-get install -y docker.io); fi",
      "PREFIX=\"${var.docker_image_prefix}\"",
      "REG=$(echo \"$PREFIX\" | sed -E 's|^(https?://)?([^/]+).*|\\2|')",
      "if [ -n \"$REG\" ]; then",
      "  if echo \"$PREFIX\" | grep -qE '^http://'; then INSECURE=1;",
      "  elif echo \"$PREFIX\" | grep -qE '^[^/]+:[0-9]+' && ! echo \"$PREFIX\" | grep -qE '^https://'; then INSECURE=1; else INSECURE=0; fi",
      "  if [ \"$INSECURE\" = 1 ]; then sudo mkdir -p /etc/docker; printf '{\"insecure-registries\":[\"%s\"]}\n' \"$REG\" | sudo tee /etc/docker/daemon.json >/dev/null; sudo systemctl restart docker; fi",
      "fi",
      "HTTP_PROXY=\"${var.docker_http_proxy}\"",
      "HTTPS_PROXY=\"${var.docker_https_proxy}\"",
      "NO_PROXY=\"${var.docker_no_proxy}\"",
      "if [ -n \"$HTTP_PROXY$HTTPS_PROXY$NO_PROXY\" ]; then",
      "  sudo mkdir -p /etc/systemd/system/docker.service.d",
      "  printf '[Service]\n' | sudo tee /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTP_PROXY\" ] && echo \"Environment=\"\"HTTP_PROXY=$HTTP_PROXY\"\"\" | sudo tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTPS_PROXY\" ] && echo \"Environment=\"\"HTTPS_PROXY=$HTTPS_PROXY\"\"\" | sudo tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$NO_PROXY\" ] && echo \"Environment=\"\"NO_PROXY=$NO_PROXY\"\"\" | sudo tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  sudo systemctl daemon-reload",
      "  sudo systemctl restart docker",
      "fi",
      "sudo apt-get install -y unzip gnupg lsb-release",
      "curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo apt-key add -",
      "CODENAME=$(lsb_release -cs); echo \"deb [arch=amd64] https://apt.releases.hashicorp.com $CODENAME main\" | sudo tee /etc/apt/sources.list.d/hashicorp.list >/dev/null",
      "sudo apt-get update -y",
      "sudo apt-get install -y consul nomad",
      "sudo mkdir -p /etc/consul.d /etc/nomad.d",
      "sudo mkdir -p /var/lib/consul /var/lib/nomad",
      "sudo chown -R consul:consul /var/lib/consul /etc/consul.d",
      "sudo chown -R nomad:nomad /var/lib/nomad /etc/nomad.d",
      "sudo mv /tmp/consul.json /etc/consul.d/consul.json",
      "sudo mv /tmp/nomad.json /etc/nomad.d/nomad.json",
      "sudo find /etc/nomad.d -type f ! -name 'nomad.json' -delete || true",
      "sudo systemctl enable consul",
      "sudo systemctl restart consul",
      "sudo systemctl enable nomad",
      "sudo systemctl restart nomad",
      "sudo mkdir -p /clickhouse/data",
      "sudo mkdir -p /etc/systemd/resolved.conf.d/",
      "echo '[Resolve]\nDNS=127.0.0.1:8600\nDNSSEC=false' | sudo tee /etc/systemd/resolved.conf.d/consul.conf > /dev/null",
      "sudo systemctl restart systemd-resolved"
    ]
  }
}