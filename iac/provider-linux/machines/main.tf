locals {
  server_ips       = [for s in var.servers : s.host]
  bootstrap_expect = length(var.servers)
}

resource "null_resource" "servers" {
  for_each = { for s in var.servers : s.host => s }

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
    content     = jsonencode({ datacenter = var.datacenter, data_dir = "/var/lib/nomad", bind_addr = "0.0.0.0", server = { enabled = true, bootstrap_expect = local.bootstrap_expect }, consul = { address = "127.0.0.1:8500" } })
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
      "curl -fsSL https://get.docker.com | sh",
      "PREFIX=\"${var.docker_image_prefix}\"",
      "if [ -n \"$PREFIX\" ] && echo \"$PREFIX\" | grep -qE '^http://'; then REG=$(echo \"$PREFIX\" | sed -E 's|^http://([^/]+).*|\\1|'); sudo mkdir -p /etc/docker; printf '{\"insecure-registries\":[\"%s\"]}\n' \"$REG\" | sudo tee /etc/docker/daemon.json >/dev/null; sudo systemctl restart docker; fi",
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

resource "null_resource" "clients" {
  for_each = { for c in var.clients : c.host => c }

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
    content     = jsonencode({ datacenter = var.datacenter, data_dir = "/var/lib/nomad", bind_addr = "0.0.0.0", client = { enabled = true, node_pool = each.value.node_pool }, consul = { address = "127.0.0.1:8500" } })
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
      "curl -fsSL https://get.docker.com | sh",
      "PREFIX=\"${var.docker_image_prefix}\"",
      "if [ -n \"$PREFIX\" ] && echo \"$PREFIX\" | grep -qE '^http://'; then REG=$(echo \"$PREFIX\" | sed -E 's|^http://([^/]+).*|\\1|'); sudo mkdir -p /etc/docker; printf '{\"insecure-registries\":[\"%s\"]}\n' \"$REG\" | sudo tee /etc/docker/daemon.json >/dev/null; sudo systemctl restart docker; fi",
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