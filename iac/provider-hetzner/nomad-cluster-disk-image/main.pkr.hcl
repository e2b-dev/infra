/**
 * Hetzner Snapshot Builder — base + role-specific images
 *
 * 1:1 functional replacement for provider-aws/nomad-cluster-disk-image/main.pkr.hcl
 * but uses hetznercloud/hcloud Packer plugin to produce Hetzner Cloud Snapshots
 * (analog to AWS AMIs).
 *
 * Snapshots labeled with `purpose=<role>` and `family=<prefix>` so the
 * NX.2.3 nodepool modules can find them via `with_selector`.
 *
 * Usage:
 *   packer init main.pkr.hcl
 *   packer build -var-file=variables.pkr.hcl main.pkr.hcl
 *
 * Build time: ~10-15 min per snapshot (depends on package install).
 */

packer {
  required_version = ">=1.10.0"

  required_plugins {
    hcloud = {
      version = ">= 1.5.0"
      source  = "github.com/hashicorp/hcloud"
    }
  }
}

# ─────────────────────────── Sources ───────────────────────────
# Each source produces a snapshot for a specific role.
# Hetzner Cloud Snapshots are global within an account (no region binding).

source "hcloud" "base" {
  token         = var.hetzner_api_token
  image         = var.base_image_slug
  location      = var.location
  server_type   = var.builder_server_type
  ssh_username  = "root"

  snapshot_name = "${var.prefix}orch-base-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  snapshot_labels = {
    purpose = "base"
    family  = "${var.prefix}orch"
    built   = formatdate("YYYY-MM-DD", timestamp())
  }
}

source "hcloud" "api" {
  token        = var.hetzner_api_token
  image        = var.base_image_slug
  location     = var.location
  server_type  = var.builder_server_type
  ssh_username = "root"

  snapshot_name = "${var.prefix}orch-api-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  snapshot_labels = {
    purpose = "api"
    family  = "${var.prefix}orch"
    built   = formatdate("YYYY-MM-DD", timestamp())
  }
}

source "hcloud" "clickhouse" {
  token        = var.hetzner_api_token
  image        = var.base_image_slug
  location     = var.location
  server_type  = var.builder_server_type
  ssh_username = "root"

  snapshot_name = "${var.prefix}orch-clickhouse-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  snapshot_labels = {
    purpose = "clickhouse"
    family  = "${var.prefix}orch"
    built   = formatdate("YYYY-MM-DD", timestamp())
  }
}

source "hcloud" "client" {
  token        = var.hetzner_api_token
  image        = var.base_image_slug
  location     = var.location
  server_type  = var.builder_server_type_client # CCX for KVM/nested-virt
  ssh_username = "root"

  snapshot_name = "${var.prefix}orch-client-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  snapshot_labels = {
    purpose = "client"
    family  = "${var.prefix}orch"
    built   = formatdate("YYYY-MM-DD", timestamp())
  }
}

source "hcloud" "control_server" {
  token        = var.hetzner_api_token
  image        = var.base_image_slug
  location     = var.location
  server_type  = var.builder_server_type
  ssh_username = "root"

  snapshot_name = "${var.prefix}orch-control-server-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  snapshot_labels = {
    purpose = "control-server"
    family  = "${var.prefix}orch"
    built   = formatdate("YYYY-MM-DD", timestamp())
  }
}

# ─────────────────────────── Build ───────────────────────────

build {
  sources = [
    "source.hcloud.base",
    "source.hcloud.api",
    "source.hcloud.clickhouse",
    "source.hcloud.client",
    "source.hcloud.control_server",
  ]

  # ─── Common provisioning (all images) ───

  provisioner "shell" {
    inline = [
      "set -euo pipefail",
      "export DEBIAN_FRONTEND=noninteractive",
      "apt-get update",
      "apt-get install -y curl wget jq unzip gnupg ca-certificates lsb-release software-properties-common",
      "apt-get install -y nfs-common net-tools build-essential",
    ]
  }

  # MinIO Client for Hetzner Object Storage access
  provisioner "shell" {
    inline = [
      "curl -sfL https://dl.min.io/client/mc/release/linux-amd64/mc -o /usr/local/bin/mc",
      "chmod +x /usr/local/bin/mc",
    ]
  }

  # Docker (rolled into base — needed by api + client + clickhouse)
  provisioner "shell" {
    inline = [
      "curl -fsSL https://get.docker.com | sh",
      "systemctl enable docker",
    ]
  }

  # Consul + Nomad binaries (HashiCorp APT repo)
  provisioner "shell" {
    inline = [
      "wget -O- https://apt.releases.hashicorp.com/gpg | gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg",
      "echo \"deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main\" > /etc/apt/sources.list.d/hashicorp.list",
      "apt-get update",
      "apt-get install -y consul=${var.consul_version}* nomad=${var.nomad_version}*",
      "mkdir -p /opt/consul/bin /opt/nomad/bin",
    ]
  }

  # ─── Role-specific provisioning (only for relevant source) ───

  # Client (Firecracker-Host) gets KVM tools + jailer
  provisioner "shell" {
    only   = ["hcloud.client"]
    inline = [
      "apt-get install -y qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-clients",
      "modprobe kvm",
      "echo 'kvm' >> /etc/modules-load.d/kvm.conf",
    ]
  }

  # ClickHouse server install
  provisioner "shell" {
    only   = ["hcloud.clickhouse"]
    inline = [
      "curl -fsSL https://packages.clickhouse.com/rpm/lts/repodata/repomd.xml.key | gpg --dearmor -o /usr/share/keyrings/clickhouse-keyring.gpg",
      "echo 'deb [signed-by=/usr/share/keyrings/clickhouse-keyring.gpg] https://packages.clickhouse.com/deb stable main' > /etc/apt/sources.list.d/clickhouse.list",
      "apt-get update",
      "DEBIAN_FRONTEND=noninteractive apt-get install -y clickhouse-server clickhouse-client",
      "systemctl enable clickhouse-server",
    ]
  }

  # Redis (built separately, see modules/redis snapshot — same builder config)
  # API server gets caddy/traefik for ingress reverse-proxy
  provisioner "shell" {
    only   = ["hcloud.api"]
    inline = [
      "curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg",
      "curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list",
      "apt-get update",
      "apt-get install -y caddy",
    ]
  }

  # Common cleanup
  provisioner "shell" {
    inline = [
      "apt-get clean",
      "rm -rf /var/lib/apt/lists/*",
      "rm -rf /tmp/*",
      "history -c || true",
    ]
  }
}
