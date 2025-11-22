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
      "curl -fsSL https://get.docker.com | sh",
      "sudo apt-get update -y",
      "sudo apt-get install -y unzip gnupg lsb-release",
      "curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo apt-key add -",
      "sudo apt-add-repository \"deb [arch=amd64] https://apt.releases.hashicorp.com $(lsb_release -cs) main\"",
      "sudo apt-get update -y",
      "sudo apt-get install -y consul nomad",
      "sudo mkdir -p /etc/consul.d /etc/nomad.d",
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
      "curl -fsSL https://get.docker.com | sh",
      "sudo apt-get update -y",
      "sudo apt-get install -y unzip gnupg lsb-release",
      "curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo apt-key add -",
      "sudo apt-add-repository \"deb [arch=amd64] https://apt.releases.hashicorp.com $(lsb_release -cs) main\"",
      "sudo apt-get update -y",
      "sudo apt-get install -y consul nomad",
      "sudo mkdir -p /etc/consul.d /etc/nomad.d",
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