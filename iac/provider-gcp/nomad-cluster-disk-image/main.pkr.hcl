packer {
  required_version = ">=1.8.4"
  required_plugins {
    googlecompute = {
      version = "1.0.16"
      source  = "github.com/hashicorp/googlecompute"
    }
  }
}

source "googlecompute" "orch" {
  image_family = "${var.prefix}orch"

  # TODO: Overwrite the image instead of creating timestamped images every time we build its
  image_name    = "${var.prefix}orch-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  project_id    = var.gcp_project_id
  source_image  = "ubuntu-2404-noble-amd64-v20260402"
  ssh_username  = "ubuntu"
  zone          = var.gcp_zone
  disk_size     = 10
  disk_type     = "pd-ssd"

  # This is used only for building the image and the GCE VM is then deleted
  machine_type = "n1-standard-4"

  # Enable nested virtualization
  image_licenses = ["projects/vm-options/global/licenses/enable-vmx"]

  # Enable IAP for SSH
  network    = var.network_name
  subnetwork = "${var.network_name}-subnetwork"
  use_iap    = true
}

locals {
  shared_setup_dir = "${path.root}/../../nomad-cluster-disk-image/setup"
}

build {
  sources = ["source.googlecompute.orch"]

  provisioner "file" {
    source      = "${local.shared_setup_dir}/supervisord.conf"
    destination = "/tmp/supervisord.conf"
  }

  provisioner "file" {
    source      = "${local.shared_setup_dir}"
    destination = "/tmp"
  }

  provisioner "file" {
    source      = "${local.shared_setup_dir}/daemon.json"
    destination = "/tmp/daemon.json"
  }

  provisioner "file" {
    source      = "${local.shared_setup_dir}/limits.conf"
    destination = "/tmp/limits.conf"
  }

  # Install Docker
  provisioner "shell" {
    inline = [
      "sudo mkdir -p /etc/docker",
      "sudo mv /tmp/daemon.json /etc/docker/daemon.json",
      "sudo curl -fsSL https://get.docker.com -o get-docker.sh",
      "sudo sh get-docker.sh",
    ]
  }

  # Install gcsfuse using signed-by keyring (required for Ubuntu 24.04+).
  # See https://cloud.google.com/storage/docs/gcsfuse-install
  provisioner "shell" {
    inline_shebang = "/bin/bash"
    inline = [
      "set -eo pipefail",
      "export GCSFUSE_REPO=gcsfuse-$(lsb_release -c -s)",
      "curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | sudo tee /usr/share/keyrings/cloud.google.asc > /dev/null",
      "echo \"deb [signed-by=/usr/share/keyrings/cloud.google.asc] https://packages.cloud.google.com/apt $GCSFUSE_REPO main\" | sudo tee /etc/apt/sources.list.d/gcsfuse.list",
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y unzip jq net-tools qemu-utils gcsfuse make build-essential openssh-client openssh-server", # TODO: openssh-server is updated to prevent security vulnerabilities
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo apt-get -y update",
      "sudo apt-get install -y nfs-common",
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo snap install go --classic"
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo systemctl start docker",
      "sudo usermod -aG docker $USER",
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo mkdir -p /opt/gruntwork",
      "git clone --branch v0.1.3 https://github.com/gruntwork-io/bash-commons.git /tmp/bash-commons",
      "sudo cp -r /tmp/bash-commons/modules/bash-commons/src /opt/gruntwork/bash-commons",
    ]
  }

  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-consul.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.consul_version}"
  }

  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-nomad.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.nomad_version}"
  }

  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-vault.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.vault_version}"
  }

  # Install the ClickHouse client at the same version as the server so it's
  # available on every node without being downloaded at boot time.
  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-clickhouse-client.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.clickhouse_client_version}"
  }

  # Install CNI plugins (needed by Nomad bridge-mode networking on the
  # ClickHouse nodepool). Harmless on nodes that don't use them.
  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-cni-plugins.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.cni_plugin_version}"
  }

  provisioner "shell" {
    inline = [
      "sudo mkdir -p /opt/nomad/plugins",
    ]
  }

  provisioner "file" {
    source      = "${path.root}/setup/gc-ops.config.yaml"
    destination = "/tmp/gc-ops.config.yaml"
  }

  provisioner "shell" {
    inline = [
      "sudo curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh",
      "sudo bash add-google-cloud-ops-agent-repo.sh --also-install",
      "sudo mkdir -p /etc/google-cloud-ops-agent",
      "sudo mv /tmp/gc-ops.config.yaml /etc/google-cloud-ops-agent/config.yaml",
    ]
  }

  provisioner "shell" {
    inline = [
      # Increase the maximum number of open files
      "sudo mv /tmp/limits.conf /etc/security/limits.conf",
      # Increase the maximum number of connections by 4x
      "echo 'net.netfilter.nf_conntrack_max = 2097152' | sudo tee -a /etc/sysctl.conf",
    ]
  }

  # Block GCE's gce-resolved.conf to prevent DNS conflicts with Consul
  provisioner "shell" {
    inline = [
      "echo 'Blocking gce-resolved.conf to prevent DNS conflicts with Consul DNS'",
      "sudo dpkg-divert --add --rename --divert /etc/systemd/resolved.conf.d/gce-resolved.conf.diverted /etc/systemd/resolved.conf.d/gce-resolved.conf || true",
      "echo 'dpkg-divert configured successfully'",
    ]
  }
}
