locals {
  server_ips       = [for s in var.servers : s.host]
  bootstrap_expect = length(var.servers)
  client_map       = { for c in var.clients : c.host => c }
  all_nodes = merge(
    { for s in var.servers : s.host => {
      host                 = s.host
      ssh_user             = s.ssh_user
      ssh_private_key_path = s.ssh_private_key_path
      role                 = "server"
      node_pool            = ""
    } },
    { for c in var.clients : c.host => {
      host                 = c.host
      ssh_user             = c.ssh_user
      ssh_private_key_path = c.ssh_private_key_path
      role                 = "client"
      node_pool            = c.node_pool
    } }
  )
  nodes_for_consul_nomad = merge(
    { for c in var.clients : c.host => {
      host                 = c.host
      ssh_user             = c.ssh_user
      ssh_private_key_path = c.ssh_private_key_path
      role                 = "client"
      node_pool            = c.node_pool
    } },
    { for s in var.servers : s.host => {
      host                 = s.host
      ssh_user             = s.ssh_user
      ssh_private_key_path = s.ssh_private_key_path
      role                 = "server"
      node_pool            = ""
    } }
  )
}

resource "null_resource" "nodes_base" {
  for_each = var.enable_nodes_uninstall ? {} : local.all_nodes

  triggers = {
    base_config_version = var.base_config_version
    docker_image_prefix = var.docker_image_prefix
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "export DEBIAN_FRONTEND=noninteractive",
      "if [ \"$(id -u)\" -eq 0 ]; then :; else if ! sudo -n true 2>/dev/null; then echo 'Passwordless sudo required for provisioning. Configure /etc/sudoers.d/$(whoami) or connect as root.'; exit 1; fi; fi",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; SUDO_E=\"\"; else SUDO=\"sudo\"; SUDO_E=\"sudo -E\"; fi",

      "$SUDO_E apt-get update -y",
      "$SUDO_E apt-get install -y curl unzip gnupg ca-certificates lsb-release",
      "if [ ! -f /usr/share/keyrings/hashicorp-archive-keyring.gpg ] || [ ! -f /etc/apt/sources.list.d/hashicorp.list ]; then curl -fsSL https://apt.releases.hashicorp.com/gpg | $SUDO gpg --dearmor --batch --yes | $SUDO tee /usr/share/keyrings/hashicorp-archive-keyring.gpg >/dev/null; CODENAME=$(lsb_release -cs); echo \"deb [arch=amd64 signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $CODENAME main\" | $SUDO tee /etc/apt/sources.list.d/hashicorp.list >/dev/null; $SUDO_E apt-get update -y; fi",
      "if ! command -v consul >/dev/null 2>&1; then $SUDO_E apt-get install -y consul; fi",
      "if ! command -v nomad  >/dev/null 2>&1; then $SUDO_E apt-get install -y nomad;  fi",
      "if ! command -v docker >/dev/null 2>&1; then (curl -fsSL https://get.docker.com | sh) || ($SUDO_E apt-get update -y && $SUDO_E apt-get install -y docker.io); fi",
      "ROLE=\"${each.value.role}\"",
      "CLIENT_NP=\"${each.value.node_pool}\"",
      "REQUIRE_NBD=$( [ \"$ROLE\" = client ] && { [ \"$CLIENT_NP\" = \"${var.builder_node_pool}\" ] || [ \"$CLIENT_NP\" = \"${var.orchestrator_node_pool}\" ]; } && echo 1 || echo 0 )",
      "UNAME=$(uname -r)",
      "if [ \"$REQUIRE_NBD\" = 1 ]; then PKG_EXTRA=linux-modules-extra-$UNAME; PKG_BASE=linux-modules-$UNAME; if apt-cache show $PKG_EXTRA >/dev/null 2>&1; then $SUDO_E apt-get install -y $PKG_EXTRA || true; elif apt-cache show $PKG_BASE >/dev/null 2>&1; then $SUDO_E apt-get install -y $PKG_BASE || true; else echo \"no matching linux-modules package for $UNAME\"; fi; $SUDO_E apt-get install -y nbd-client || true; fi",
    ]
  }
}

resource "null_resource" "nodes_docker_proxy" {
  for_each = (var.enable_nodes_uninstall || !var.enable_nodes_docker_proxy) ? {} : local.all_nodes

  triggers = {
    docker_http_proxy           = var.docker_http_proxy
    docker_https_proxy          = var.docker_https_proxy
    docker_no_proxy             = var.docker_no_proxy
    docker_image_prefix         = var.docker_image_prefix
    docker_proxy_config_version = var.docker_proxy_config_version
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; else SUDO=\"sudo\"; fi",
      "PREFIX=\"${var.docker_image_prefix}\"",
      "REG=$(echo \"$PREFIX\" | sed -E 's|^(https?://)?([^/]+).*|\\2|')",
      "if [ -n \"$REG\" ]; then",
      "  if echo \"$PREFIX\" | grep -qE '^http://'; then SCHEME=\"http\"; INSECURE=1;",
      "  elif echo \"$PREFIX\" | grep -qE '^[^/]+:[0-9]+' && ! echo \"$PREFIX\" | grep -qE '^https://'; then SCHEME=\"http\"; INSECURE=1;",
      "  elif echo \"$PREFIX\" | grep -qE '^https://'; then SCHEME=\"https\"; INSECURE=0;",
      "  else SCHEME=\"https\"; INSECURE=0; fi",
      "  MIRROR_URL=\"$SCHEME://$REG\"",
      "  $SUDO mkdir -p /etc/docker",
      "  if [ \"$INSECURE\" = 1 ]; then",
      "    printf '{\"insecure-registries\":[\"%s\"],\"registry-mirrors\":[\"%s\"]}\\n' \"$REG\" \"$MIRROR_URL\" | $SUDO tee /etc/docker/daemon.json >/dev/null",
      "  else",
      "    printf '{\"registry-mirrors\":[\"%s\"]}\\n' \"$MIRROR_URL\" | $SUDO tee /etc/docker/daemon.json >/dev/null",
      "  fi",
      "  $SUDO systemctl restart docker",
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
      "fi"
    ]
  }

  depends_on = [null_resource.nodes_base]
}

resource "null_resource" "nodes_consul_nomad" {
  for_each = var.enable_nodes_uninstall ? {} : local.nodes_for_consul_nomad

  triggers = {
    consul_config_version = var.consul_config_version
    nomad_config_version  = var.nomad_config_version
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
        bind_addr   = each.value.host,
        client_addr = "0.0.0.0",
        retry_join  = local.server_ips,
        recursors   = ["8.8.8.8", "1.1.1.1"]
      },
      { server = contains(local.server_ips, each.value.host) },
      contains(local.server_ips, each.value.host) ? { bootstrap_expect = local.bootstrap_expect } : {},
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
        consul     = length(var.consul_acl_token) > 0 ? { address = "127.0.0.1:8500", token = var.consul_acl_token } : { address = "127.0.0.1:8500" },
        telemetry = {
          publish_allocation_metrics = true
          publish_node_metrics       = true
          prometheus_metrics         = true
          collection_interval        = "1s"
        }
      },
      length(var.nomad_acl_token) > 0 ? { acl = { enabled = true } } : {},
      contains(local.server_ips, each.value.host) ? { server = { enabled = true, bootstrap_expect = local.bootstrap_expect } } : {},
      contains(keys(local.client_map), each.value.host) ? { client = { enabled = true, node_pool = local.client_map[each.value.host].node_pool, servers = [for s in local.server_ips : "${s}:4647"], options = { "driver.raw_exec.enable" = "1" } } } : {}
    ))
    destination = "/tmp/nomad.json"
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; else SUDO=\"sudo\"; fi",
      "$SUDO systemctl stop consul || true",
      "$SUDO systemctl stop nomad || true",

      "$SUDO mkdir -p /etc/consul.d /etc/nomad.d",
      "$SUDO mkdir -p /var/lib/consul /var/lib/nomad",
      "$SUDO chown -R consul:consul /var/lib/consul /etc/consul.d",
      "$SUDO chown -R nomad:nomad /var/lib/nomad /etc/nomad.d",
      "$SUDO mv /tmp/consul.json /etc/consul.d/consul.json",
      "$SUDO mv /tmp/nomad.json /etc/nomad.d/nomad.json",

      "$SUDO systemctl daemon-reload",
      "$SUDO systemctl enable consul",
      "$SUDO systemctl restart consul || true",
      "for i in $(seq 1 12); do $SUDO systemctl is-active consul >/dev/null 2>&1 && break || sleep 2; done",
      "$SUDO systemctl is-active consul >/dev/null 2>&1 || (echo consul failed to start; $SUDO journalctl -xeu consul.service | tail -n 100; exit 1)",

      "for i in $(seq 1 12); do curl -sSf http://127.0.0.1:8500/v1/status/leader >/dev/null 2>&1 && break || sleep 2; done",
      "curl -sSf http://127.0.0.1:8500/v1/status/leader >/dev/null 2>&1 || (echo consul http api not ready; $SUDO journalctl -xeu consul.service | tail -n 100; exit 1)",

      "$SUDO systemctl enable nomad",
      "$SUDO systemctl restart nomad",
      "for i in $(seq 1 12); do $SUDO systemctl is-active nomad >/dev/null 2>&1 && break || sleep 2; done",
      "curl -sSf http://127.0.0.1:4646/v1/agent/self >/dev/null 2>&1 || (echo nomad http api not ready; $SUDO journalctl -xeu nomad.service | tail -n 100; exit 1)"
    ]
  }

  depends_on = [null_resource.nodes_base]
}

resource "null_resource" "servers_node_pools" {
  for_each = var.enable_nodes_uninstall ? {} : ((var.builder_node_pool != "" || var.orchestrator_node_pool != "") ? { for s in var.servers : s.host => s } : {})

  triggers = {
    node_pools_config_version = var.node_pools_config_version
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; else SUDO=\"sudo\"; fi",
      "TOKEN=\"${var.nomad_acl_token}\"",
      "if [ -z \"$TOKEN\" ]; then",
      "  for i in $(seq 1 20); do curl -sSf http://127.0.0.1:4646/v1/agent/health >/dev/null 2>&1 && break || sleep 2; done",
      "  OUT=$($SUDO nomad acl bootstrap -json 2>/dev/null || true)",
      "  echo nomad acl bootstrap -json output: $OUT",
      "  TOKEN=$(echo \"$OUT\" | sed -n 's/.*\"SecretID\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p')",
      "  if [ -z \"$TOKEN\" ]; then",
      "    OUT=$($SUDO nomad acl bootstrap 2>/dev/null || true)",
      "    TOKEN=$(echo \"$OUT\" | awk -F= '/^ Secret ID/{gsub(/^[ \\t]+|[ \\t]+$/, \"\", $2); print $2}')",
      "  fi",
      "fi",
      "echo nomad acl token: $TOKEN",
      "printf 'node_pool \"${var.api_node_pool}\" {\\n  description = \"Nodes for api.\"\\n}\\n' | $SUDO tee /tmp/api_node_pool.hcl >/dev/null",
      "printf 'node_pool \"${var.builder_node_pool}\" {\\n  description = \"Nodes for template builds.\"\\n}\\n' | $SUDO tee /tmp/build_node_pool.hcl >/dev/null",
      "printf 'node_pool \"${var.orchestrator_node_pool}\" {\\n  description = \"Nodes for orchestrator.\"\\n}\\n' | $SUDO tee /tmp/orchestrator_node_pool.hcl >/dev/null",
      "if [ -n \"$TOKEN\" ]; then $SUDO nomad node pool apply -token \"$TOKEN\" /tmp/api_node_pool.hcl; else $SUDO nomad node pool apply /tmp/api_node_pool.hcl; fi",
      "if [ -n \"$TOKEN\" ]; then $SUDO nomad node pool apply -token \"$TOKEN\" /tmp/build_node_pool.hcl; else $SUDO nomad node pool apply /tmp/build_node_pool.hcl; fi",
      "if [ -n \"$TOKEN\" ]; then $SUDO nomad node pool apply -token \"$TOKEN\" /tmp/orchestrator_node_pool.hcl; else $SUDO nomad node pool apply /tmp/orchestrator_node_pool.hcl; fi"
    ]
  }

  depends_on = [null_resource.nodes_consul_nomad]
}

resource "null_resource" "nodes_dns" {
  for_each = var.enable_nodes_uninstall ? {} : local.all_nodes

  triggers = {
    dns_config_version = var.dns_config_version
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; else SUDO=\"sudo\"; fi",
      "$SUDO mkdir -p /etc/systemd/resolved.conf.d/",
      "printf '[Resolve]\\nDNS=127.0.0.1:8600\\nDomains=~consul\\nDNSSEC=false\\n' | $SUDO tee /etc/systemd/resolved.conf.d/consul.conf >/dev/null",
      "printf '[Resolve]\\nDNSStubListener=yes\\nDNSStubListenerExtra=172.17.0.1\\n' | $SUDO tee /etc/systemd/resolved.conf.d/docker.conf >/dev/null",
      "for i in $(seq 1 10); do echo > /dev/tcp/127.0.0.1/8600 2>/dev/null && break || sleep 1; done",
      "echo > /dev/tcp/127.0.0.1/8600 2>/dev/null || (echo \"Consul DNS (port 8600) not reachable\"; exit 1)",
      "$SUDO systemctl restart systemd-resolved"
    ]
  }

  depends_on = [null_resource.nodes_consul_nomad]
}


resource "null_resource" "nodes_fc_artifacts" {
  for_each = (var.enable_nodes_uninstall || !var.enable_nodes_fc_artifacts) ? {} : local.all_nodes

  triggers = {
    fc_artifacts_version        = var.fc_artifacts_version
    kernel_source_base_url      = var.kernel_source_base_url
    firecracker_source_base_url = var.firecracker_source_base_url
    default_kernel_version      = var.default_kernel_version
    default_firecracker_version = var.default_firecracker_version
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; else SUDO=\"sudo\"; fi",
      "ROLE=\"${each.value.role}\"",
      "CLIENT_NP=\"${each.value.node_pool}\"",
      "REQUIRE_FC_ARTIFACTS=$( [ \"$ROLE\" = client ] && { [ \"$CLIENT_NP\" = \"${var.builder_node_pool}\" ] || [ \"$CLIENT_NP\" = \"${var.orchestrator_node_pool}\" ]; } && echo 1 || echo 0 )",
      "if [ \"$REQUIRE_FC_ARTIFACTS\" = 1 ]; then",
      "  KBASE=\"${var.kernel_source_base_url}\"",
      "  FBASE=\"${var.firecracker_source_base_url}\"",
      "  $SUDO mkdir -p /orchestrator/sandbox /orchestrator/template /orchestrator/build /fc-vm /fc-kernels /fc-versions",
      "  if ! command -v wget >/dev/null 2>&1; then $SUDO apt-get update -y && $SUDO apt-get install -y wget; fi",
      "  if [ -n \"$KBASE\" ]; then",
      "    CUT_K=$(echo \"$KBASE\" | sed -E 's|https?://[^/]+/||' | awk -F/ '{print NF}')",
      "    wget -q -r -np -nH --cut-dirs=\"$CUT_K\" --reject \"index.html*\" -P /fc-kernels \"$KBASE/\"",
      "  fi",
      "  if [ -n \"$FBASE\" ]; then",
      "    CUT_F=$(echo \"$FBASE\" | sed -E 's|https?://[^/]+/||' | awk -F/ '{print NF}')",
      "    wget -q -r -np -nH --cut-dirs=\"$CUT_F\" --reject \"index.html*\" -P /fc-versions \"$FBASE/\"",
      "    find /fc-versions -type f -name firecracker -exec $SUDO chmod +x {} \\;",
      "  fi",
      "fi"
    ]
  }

  depends_on = [null_resource.nodes_base]
}

resource "null_resource" "uninstall_safety_check" {
  count = (var.enable_nodes_uninstall && length(var.uninstall_confirm_phrase) > 0) ? 1 : 0

  triggers = {
    phrase = var.uninstall_confirm_phrase
  }

  provisioner "local-exec" {
    command     = <<EOT
      PHRASE="${var.uninstall_confirm_phrase}"
      CURRENT=$(date +%Y%m%d%H%M)
      
      if [ "$PHRASE" = "$CURRENT" ]; then exit 0; fi

      # Try BSD date (macOS)
      if date -v-1M >/dev/null 2>&1; then
        PREV=$(date -v-1M +%Y%m%d%H%M)
        NEXT=$(date -v+1M +%Y%m%d%H%M)
      else
        # Try GNU date (Linux)
        PREV=$(date -d '1 minute ago' +%Y%m%d%H%M)
        NEXT=$(date -d '1 minute' +%Y%m%d%H%M)
      fi

      if [ "$PHRASE" = "$PREV" ] || [ "$PHRASE" = "$NEXT" ]; then exit 0; fi

      echo "Error: Uninstall phrase '$PHRASE' does not match current time ($CURRENT) +/- 1 minute."
      exit 1
    EOT
    interpreter = ["/bin/bash", "-c"]
  }
}

resource "null_resource" "nodes_uninstall" {
  for_each = (var.enable_nodes_uninstall && length(var.uninstall_confirm_phrase) > 0) ? local.all_nodes : {}

  triggers = {
    uninstall_version = var.uninstall_version
  }

  connection {
    type        = "ssh"
    host        = each.value.host
    user        = each.value.ssh_user
    private_key = file(each.value.ssh_private_key_path)
  }

  provisioner "file" {
    content     = file("${path.module}/../scripts/uninstall_provider_linux.sh")
    destination = "/tmp/uninstall_provider_linux.sh"
  }

  provisioner "remote-exec" {
    inline = [
      "set -e",
      "if [ \"$(id -u)\" -eq 0 ]; then SUDO=\"\"; else SUDO=\"sudo\"; fi",
      "$SUDO chmod +x /tmp/uninstall_provider_linux.sh",
      "FORCE_UNINSTALL=1 bash /tmp/uninstall_provider_linux.sh"
    ]
  }

  depends_on = [null_resource.uninstall_safety_check]
}
