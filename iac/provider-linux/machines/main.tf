locals {
  server_ips       = [for s in var.servers : s.host]
  bootstrap_expect = length(var.servers)
  client_map       = { for c in var.clients : c.host => c }
}

resource "null_resource" "servers" {
  for_each = { for s in var.servers : s.host => s }

  triggers = {
    docker_http_proxy      = var.docker_http_proxy
    docker_https_proxy     = var.docker_https_proxy
    docker_no_proxy        = var.docker_no_proxy
    docker_image_prefix    = var.docker_image_prefix
    driver_raw_exec_enable = "1"
    nbd_config_version     = "v2"
    server_config_version  = "v2"
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
          servers   = [for s in local.server_ips : "${s}:4647"],
          options   = { "driver.raw_exec.enable" = "1" }
        }
      } : {}
    ))
    destination = "/tmp/nomad.json"
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then :; else if ! sudo -n true 2>/dev/null; then echo 'Passwordless sudo required for provisioning. Configure /etc/sudoers.d/$(whoami) or connect as root.'; exit 1; fi; fi",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; SUDO_E=\"\"; else SUDO=\"sudo\"; SUDO_E=\"sudo -E\"; fi",
      "REQUIRE_NBD=${contains(keys(local.client_map), each.value.host) && (local.client_map[each.value.host].node_pool == var.builder_node_pool || local.client_map[each.value.host].node_pool == var.orchestrator_node_pool) ? "1" : "0"}",
      "export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a",
      "while $SUDO fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/dpkg/lock >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do sleep 5; done",
      "$SUDO_E apt-get update -y",
      "$SUDO_E apt-get install -y curl",
      "USR=\"$(whoami)\"; echo \"$USR ALL=(ALL) NOPASSWD:ALL\" | $SUDO tee /etc/sudoers.d/$USR >/dev/null; $SUDO chmod 0440 /etc/sudoers.d/$USR; $SUDO usermod -aG sudo $USR",
      "if ! command -v docker >/dev/null 2>&1; then (curl -fsSL https://get.docker.com | sh) || ($SUDO_E apt-get update -y && $SUDO_E apt-get install -y docker.io); fi",
      "PREFIX=\"${var.docker_image_prefix}\"",
      "REG=$(echo \"$PREFIX\" | sed -E 's|^(https?://)?([^/]+).*|\\2|')",
      "if [ -n \"$REG\" ]; then",
      "  if echo \"$PREFIX\" | grep -qE '^http://'; then INSECURE=1;",
      "  elif echo \"$PREFIX\" | grep -qE '^[^/]+:[0-9]+' && ! echo \"$PREFIX\" | grep -qE '^https://'; then INSECURE=1; else INSECURE=0; fi",
      "  if [ \"$INSECURE\" = 1 ]; then $SUDO mkdir -p /etc/docker; printf '{\"insecure-registries\":[\"%s\"]}\n' \"$REG\" | $SUDO tee /etc/docker/daemon.json >/dev/null; $SUDO systemctl restart docker; fi",
      "fi",
      "HTTP_PROXY=\"${var.docker_http_proxy}\"",
      "HTTPS_PROXY=\"${var.docker_https_proxy}\"",
      "NO_PROXY=\"${var.docker_no_proxy}\"",
      "if [ -n \"$HTTP_PROXY$HTTPS_PROXY$NO_PROXY\" ]; then",
      "  $SUDO mkdir -p /etc/systemd/system/docker.service.d",
      "  printf '[Service]\n' | $SUDO tee /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTP_PROXY\" ] && echo \"Environment=\"\"HTTP_PROXY=$HTTP_PROXY\"\"\" | $SUDO tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTPS_PROXY\" ] && echo \"Environment=\"\"HTTPS_PROXY=$HTTPS_PROXY\"\"\" | $SUDO tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$NO_PROXY\" ] && echo \"Environment=\"\"NO_PROXY=$NO_PROXY\"\"\" | $SUDO tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  $SUDO systemctl daemon-reload",
      "  $SUDO systemctl restart docker",
      "fi",
      "$SUDO_E apt-get install -y unzip gnupg lsb-release",
      "curl -fsSL https://apt.releases.hashicorp.com/gpg | $SUDO apt-key add -",
      "CODENAME=$(lsb_release -cs); echo \"deb [arch=amd64] https://apt.releases.hashicorp.com $CODENAME main\" | $SUDO tee /etc/apt/sources.list.d/hashicorp.list >/dev/null",
      "$SUDO_E apt-get update -y",
      "while $SUDO fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/dpkg/lock >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do sleep 5; done",
      "$SUDO_E apt-get install -y consul nomad",
      "UNAME=$(uname -r)",
      "while $SUDO fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/dpkg/lock >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do sleep 5; done",
      "PKG_EXTRA=linux-modules-extra-$UNAME; PKG_BASE=linux-modules-$UNAME; if [ \"$REQUIRE_NBD\" = 1 ]; then if apt-cache show $PKG_EXTRA >/dev/null 2>&1; then $SUDO_E apt-get install -y $PKG_EXTRA || true; elif apt-cache show $PKG_BASE >/dev/null 2>&1; then $SUDO_E apt-get install -y $PKG_BASE || true; else echo \"no matching linux-modules package for $UNAME\"; fi; fi",
      "$SUDO_E apt-get install -y nbd-client || true",
      "sh -c 'CFG_HAS_NBD=$(zcat /proc/config.gz 2>/dev/null | grep -E \"^CONFIG_BLK_DEV_NBD=\" | cut -d= -f2); if $SUDO modinfo nbd >/dev/null 2>&1; then if [ \"$REQUIRE_NBD\" = 1 ]; then echo nbd | $SUDO tee /etc/modules-load.d/nbd.conf >/dev/null; echo \"options nbd nbds_max=4096 max_part=16\" | $SUDO tee /etc/modprobe.d/nbd.conf >/dev/null; $SUDO systemctl restart systemd-modules-load || true; $SUDO modprobe nbd nbds_max=4096 max_part=16 || true; fi; elif [ \"$CFG_HAS_NBD\" = y ]; then echo \"nbd built-in (y), no modprobe needed\"; elif [ \"$REQUIRE_NBD\" = 1 ]; then echo \"ERROR: nbd module missing for required node; kernel $(uname -r) lacks nbd. Install matching linux-modules or switch kernel.\"; exit 1; else $SUDO rm -f /etc/modules-load.d/nbd.conf /etc/modprobe.d/nbd.conf; echo \"nbd unavailable, skipping (not required)\"; fi'",
      "$SUDO mkdir -p /etc/consul.d /etc/nomad.d",
      "$SUDO mkdir -p /var/lib/consul /var/lib/nomad",
      "$SUDO chown -R consul:consul /var/lib/consul /etc/consul.d",
      "$SUDO chown -R nomad:nomad /var/lib/nomad /etc/nomad.d",
      "$SUDO mv /tmp/consul.json /etc/consul.d/consul.json",
      "$SUDO mv /tmp/nomad.json /etc/nomad.d/nomad.json",
      "$SUDO find /etc/nomad.d -type f ! -name 'nomad.json' -delete || true",
      "$SUDO systemctl stop consul || true",
      "$SUDO pkill -x consul || true",
      "$SUDO fuser -k 8600/udp || true",
      "$SUDO fuser -k 8600/tcp || true",
      "$SUDO systemctl enable consul",
      "$SUDO systemctl restart consul || true",
      "for i in $(seq 1 120); do $SUDO systemctl is-active consul >/dev/null 2>&1 && break || sleep 2; done",
      "$SUDO systemctl is-active consul >/dev/null 2>&1 || (echo consul failed to start; $SUDO journalctl -xeu consul.service | tail -n 100; exit 1)",
      "$SUDO systemctl enable nomad",
      "$SUDO systemctl restart nomad",
      "for i in $(seq 1 60); do $SUDO systemctl is-active nomad >/dev/null 2>&1 && break || sleep 2; done",
      "for i in $(seq 1 60); do curl -sSf http://127.0.0.1:4646/v1/agent/self >/dev/null 2>&1 && break || sleep 2; done",
      "printf 'node_pool \"api\" {\n  description = \"Nodes for api.\"\n}\n' | $SUDO tee /tmp/api_node_pool.hcl >/dev/null",
      "printf 'node_pool \"build\" {\n  description = \"Nodes for template builds.\"\n}\n' | $SUDO tee /tmp/build_node_pool.hcl >/dev/null",
      "TOKEN=\"${var.nomad_acl_token}\"; for i in $(seq 1 5); do if [ -n \"$TOKEN\" ]; then $SUDO nomad node pool apply -token \"$TOKEN\" /tmp/api_node_pool.hcl && break; else $SUDO nomad node pool apply /tmp/api_node_pool.hcl && break; fi; sleep 2; done",
      "TOKEN=\"${var.nomad_acl_token}\"; for i in $(seq 1 5); do if [ -n \"$TOKEN\" ]; then $SUDO nomad node pool apply -token \"$TOKEN\" /tmp/build_node_pool.hcl && break; else $SUDO nomad node pool apply /tmp/build_node_pool.hcl && break; fi; sleep 2; done",
      "$SUDO mkdir -p /clickhouse/data",
      "$SUDO mkdir -p /etc/systemd/resolved.conf.d/",
      "echo '[Resolve]\nDNS=127.0.0.1:8600\nDNSSEC=false' | $SUDO tee /etc/systemd/resolved.conf.d/consul.conf > /dev/null",
      "$SUDO systemctl restart systemd-resolved"
    ]
  }
}

resource "null_resource" "clients" {
  depends_on = [null_resource.servers]
  for_each   = { for c in var.clients : c.host => c if !(contains(local.server_ips, c.host)) }

  triggers = {
    docker_http_proxy      = var.docker_http_proxy
    docker_https_proxy     = var.docker_https_proxy
    docker_no_proxy        = var.docker_no_proxy
    docker_image_prefix    = var.docker_image_prefix
    driver_raw_exec_enable = "1"
    nbd_config_version     = "v2"
    server_config_version  = "v1"
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
    content     = jsonencode({ datacenter = var.datacenter, data_dir = "/var/lib/nomad", bind_addr = "0.0.0.0", client = { enabled = true, node_pool = each.value.node_pool, servers = [for s in local.server_ips : "${s}:4647"], options = { "driver.raw_exec.enable" = "1" } }, consul = { address = "127.0.0.1:8500" } })
    destination = "/tmp/nomad.json"
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then :; else if ! sudo -n true 2>/dev/null; then echo 'Passwordless sudo required for provisioning. Configure /etc/sudoers.d/$(whoami) or connect as root.'; exit 1; fi; fi",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; SUDO_E=\"\"; else SUDO=\"sudo\"; SUDO_E=\"sudo -E\"; fi",
      "REQUIRE_NBD=${contains([var.builder_node_pool, var.orchestrator_node_pool], each.value.node_pool) ? "1" : "0"}",
      "export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a",
      "while $SUDO fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/dpkg/lock >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do sleep 5; done",
      "$SUDO_E apt-get update -y",
      "$SUDO_E apt-get install -y curl",
      "USR=\"$(whoami)\"; echo \"$USR ALL=(ALL) NOPASSWD:ALL\" | $SUDO tee /etc/sudoers.d/$USR >/dev/null; $SUDO chmod 0440 /etc/sudoers.d/$USR; $SUDO usermod -aG sudo $USR",
      "if ! command -v docker >/dev/null 2>&1; then (curl -fsSL https://get.docker.com | sh) || ($SUDO_E apt-get update -y && $SUDO_E apt-get install -y docker.io); fi",
      "PREFIX=\"${var.docker_image_prefix}\"",
      "REG=$(echo \"$PREFIX\" | sed -E 's|^(https?://)?([^/]+).*|\\2|')",
      "if [ -n \"$REG\" ]; then",
      "  if echo \"$PREFIX\" | grep -qE '^http://'; then INSECURE=1;",
      "  elif echo \"$PREFIX\" | grep -qE '^[^/]+:[0-9]+' && ! echo \"$PREFIX\" | grep -qE '^https://'; then INSECURE=1; else INSECURE=0; fi",
      "  if [ \"$INSECURE\" = 1 ]; then $SUDO mkdir -p /etc/docker; printf '{\"insecure-registries\":[\"%s\"]}\n' \"$REG\" | $SUDO tee /etc/docker/daemon.json >/dev/null; $SUDO systemctl restart docker; fi",
      "fi",
      "HTTP_PROXY=\"${var.docker_http_proxy}\"",
      "HTTPS_PROXY=\"${var.docker_https_proxy}\"",
      "NO_PROXY=\"${var.docker_no_proxy}\"",
      "if [ -n \"$HTTP_PROXY$HTTPS_PROXY$NO_PROXY\" ]; then",
      "  $SUDO mkdir -p /etc/systemd/system/docker.service.d",
      "  printf '[Service]\n' | $SUDO tee /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTP_PROXY\" ] && echo \"Environment=\"\"HTTP_PROXY=$HTTP_PROXY\"\"\" | $SUDO tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$HTTPS_PROXY\" ] && echo \"Environment=\"\"HTTPS_PROXY=$HTTPS_PROXY\"\"\" | $SUDO tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  [ -n \"$NO_PROXY\" ] && echo \"Environment=\"\"NO_PROXY=$NO_PROXY\"\"\" | $SUDO tee -a /etc/systemd/system/docker.service.d/proxy.conf >/dev/null",
      "  $SUDO systemctl daemon-reload",
      "  $SUDO systemctl restart docker",
      "fi",
      "$SUDO_E apt-get install -y unzip gnupg lsb-release",
      "curl -fsSL https://apt.releases.hashicorp.com/gpg | $SUDO apt-key add -",
      "CODENAME=$(lsb_release -cs); echo \"deb [arch=amd64] https://apt.releases.hashicorp.com $CODENAME main\" | $SUDO tee /etc/apt/sources.list.d/hashicorp.list >/dev/null",
      "$SUDO_E apt-get update -y",
      "while $SUDO fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/dpkg/lock >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do sleep 5; done",
      "$SUDO_E apt-get install -y consul nomad",
      "UNAME=$(uname -r)",
      "while $SUDO fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/dpkg/lock >/dev/null 2>&1; do sleep 5; done",
      "while $SUDO fuser /var/lib/apt/lists/lock >/dev/null 2>&1; do sleep 5; done",
      "PKG_EXTRA=linux-modules-extra-$UNAME; PKG_BASE=linux-modules-$UNAME; if [ \"$REQUIRE_NBD\" = 1 ]; then if apt-cache show $PKG_EXTRA >/dev/null 2>&1; then $SUDO_E apt-get install -y $PKG_EXTRA || true; elif apt-cache show $PKG_BASE >/dev/null 2>&1; then $SUDO_E apt-get install -y $PKG_BASE || true; else echo \"no matching linux-modules package for $UNAME\"; fi; fi",
      "$SUDO_E apt-get install -y nbd-client || true",
      "sh -c 'CFG_HAS_NBD=$(zcat /proc/config.gz 2>/dev/null | grep -E \"^CONFIG_BLK_DEV_NBD=\" | cut -d= -f2); if $SUDO modinfo nbd >/dev/null 2>&1; then if [ \"$REQUIRE_NBD\" = 1 ]; then echo nbd | $SUDO tee /etc/modules-load.d/nbd.conf >/dev/null; echo \"options nbd nbds_max=4096 max_part=16\" | $SUDO tee /etc/modprobe.d/nbd.conf >/dev/null; $SUDO systemctl restart systemd-modules-load || true; $SUDO modprobe nbd nbds_max=4096 max_part=16 || true; fi; elif [ \"$CFG_HAS_NBD\" = y ]; then echo \"nbd built-in (y), no modprobe needed\"; elif [ \"$REQUIRE_NBD\" = 1 ]; then echo \"ERROR: nbd module missing for required node; kernel $(uname -r) lacks nbd. Install matching linux-modules or switch kernel.\"; exit 1; else $SUDO rm -f /etc/modules-load.d/nbd.conf /etc/modprobe.d/nbd.conf; echo \"nbd unavailable, skipping (not required)\"; fi'",
      "$SUDO mkdir -p /etc/consul.d /etc/nomad.d",
      "$SUDO mkdir -p /var/lib/consul /var/lib/nomad",
      "$SUDO chown -R consul:consul /var/lib/consul /etc/consul.d",
      "$SUDO chown -R nomad:nomad /var/lib/nomad /etc/nomad.d",
      "$SUDO mv /tmp/consul.json /etc/consul.d/consul.json",
      "if grep -q '\"server\"' /etc/nomad.d/nomad.json 2>/dev/null; then echo 'skip nomad.json overwrite'; else $SUDO mv /tmp/nomad.json /etc/nomad.d/nomad.json; fi",
      "$SUDO find /etc/nomad.d -type f ! -name 'nomad.json' -delete || true",
      "$SUDO systemctl stop consul || true",
      "$SUDO pkill -x consul || true",
      "$SUDO fuser -k 8600/udp || true",
      "$SUDO fuser -k 8600/tcp || true",
      "$SUDO systemctl enable consul",
      "$SUDO systemctl restart consul || true",
      "for i in $(seq 1 120); do $SUDO systemctl is-active consul >/dev/null 2>&1 && break || sleep 2; done",
      "$SUDO systemctl is-active consul >/dev/null 2>&1 || (echo consul failed to start; $SUDO journalctl -xeu consul.service | tail -n 100; exit 1)",
      "$SUDO systemctl enable nomad",
      "$SUDO systemctl restart nomad",
      "$SUDO mkdir -p /clickhouse/data",
      "$SUDO mkdir -p /etc/systemd/resolved.conf.d/",
      "echo '[Resolve]\nDNS=127.0.0.1:8600\nDNSSEC=false' | $SUDO tee /etc/systemd/resolved.conf.d/consul.conf > /dev/null",
      "$SUDO systemctl restart systemd-resolved"
    ]
  }
}