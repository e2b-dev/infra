packer {
  required_version = "=1.13.1"
  required_plugins {
    googlecompute = {
      version = "1.0.16"
      source  = "github.com/hashicorp/googlecompute"
    }
  }
}

locals {
  quota_policy  = jsondecode(file(abspath("${path.root}/../topology/minimal-workload-policy.json")))
  quota_reserve = local.quota_policy.transient_reserve
}

source "googlecompute" "orch" {
  image_family      = var.image_family
  image_name        = var.image_name
  image_description = "Monad operator-canary Nomad image from ${var.source_revision}"
  image_labels = {
    monad_environment = var.image_environment
    monad_revision    = var.source_revision
  }
  project_id                      = var.gcp_project_id
  source_image                    = var.source_image
  source_image_project_id         = ["ubuntu-os-cloud"]
  ssh_username                    = "ubuntu"
  zone                            = var.gcp_zone
  disk_size                       = local.quota_reserve.pd_ssd_gb
  disk_type                       = local.quota_reserve.disk_type
  disable_default_service_account = true

  # This is used only for building the image and the GCE VM is then deleted
  machine_type = local.quota_reserve.machine_type

  # Enable nested virtualization
  image_licenses = ["projects/vm-options/global/licenses/enable-vmx"]

  # Enable IAP for SSH
  network    = var.network_name
  subnetwork = var.subnet_name
  use_iap    = true
  # Reserve one regional public IP conservatively while the builder exists.
  omit_external_ip = local.quota_reserve.regional_public_ips == 0
}

locals {
  shared_setup_dir       = "${path.root}/../../nomad-cluster-disk-image/setup"
  root_artifact_lock     = jsondecode(file(abspath("${path.root}/setup/root-artifacts.lock.json")))
  root_artifact_lock_sha = sha256(file(abspath("${path.root}/setup/root-artifacts.lock.json")))
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

  # Freeze Ubuntu package resolution before any apt metadata refresh. Raw
  # third-party packages are downloaded by exact URL and verified below.
  provisioner "shell" {
    inline_shebang = "/bin/bash"
    inline = [
      "set -euo pipefail",
      "sources=/etc/apt/sources.list.d/ubuntu.sources",
      "test -f $sources",
      "sudo sed -i '/^Snapshot:/d; /^Signed-By:/a Snapshot: ${local.root_artifact_lock.ubuntu_snapshot}' $sources",
      "grep -F 'Snapshot: ${local.root_artifact_lock.ubuntu_snapshot}' $sources >/dev/null",
    ]
  }

  # Install Docker
  provisioner "shell" {
    inline_shebang = "/bin/bash"
    inline = [
      "set -euo pipefail",
      "artifact_dir=/tmp/monad-root-artifacts",
      "mkdir -m 0700 -p $artifact_dir",
      "download() { name=\"$1\"; url=\"$2\"; sha=\"$3\"; curl -fsSL --retry 5 --retry-delay 5 -o \"$artifact_dir/$name\" \"$url\"; echo \"$sha  $artifact_dir/$name\" | sha256sum --check --strict; }",
      "download containerd.deb '${local.root_artifact_lock.containerd.url}' '${local.root_artifact_lock.containerd.sha256}'",
      "download docker-cli.deb '${local.root_artifact_lock.docker_cli.url}' '${local.root_artifact_lock.docker_cli.sha256}'",
      "download docker-ce.deb '${local.root_artifact_lock.docker_ce.url}' '${local.root_artifact_lock.docker_ce.sha256}'",
      "download docker-rootless.deb '${local.root_artifact_lock.docker_rootless.url}' '${local.root_artifact_lock.docker_rootless.sha256}'",
      "download docker-buildx.deb '${local.root_artifact_lock.docker_buildx.url}' '${local.root_artifact_lock.docker_buildx.sha256}'",
      "download docker-compose.deb '${local.root_artifact_lock.docker_compose.url}' '${local.root_artifact_lock.docker_compose.sha256}'",
      "download docker-model.deb '${local.root_artifact_lock.docker_model.url}' '${local.root_artifact_lock.docker_model.sha256}'",
      "sudo mkdir -p /etc/docker",
      "sudo mv /tmp/daemon.json /etc/docker/daemon.json",
      "sudo apt-get update",
      "sudo apt-get install -y $artifact_dir/containerd.deb $artifact_dir/docker-cli.deb $artifact_dir/docker-ce.deb $artifact_dir/docker-rootless.deb $artifact_dir/docker-buildx.deb $artifact_dir/docker-compose.deb $artifact_dir/docker-model.deb",
      "docker --version | grep -F 'Docker version ${local.root_artifact_lock.docker_engine_version},'",
    ]
  }

  # Nomad and host Docker use this helper to exchange the VM's attached
  # service-account identity for short-lived Artifact Registry credentials.
  provisioner "shell" {
    inline_shebang = "/bin/bash"
    inline = [
      "set -euo pipefail",
      "helper_archive=/tmp/docker-credential-gcr.tar.gz",
      "curl -fsSL -o $helper_archive https://github.com/GoogleCloudPlatform/docker-credential-gcr/releases/download/v${local.root_artifact_lock.docker_credential_gcr.version}/docker-credential-gcr_linux_amd64-${local.root_artifact_lock.docker_credential_gcr.version}.tar.gz",
      "echo '${local.root_artifact_lock.docker_credential_gcr.sha256}  '$helper_archive | sha256sum --check --strict",
      "sudo tar -xzf $helper_archive -C /usr/local/bin docker-credential-gcr",
      "sudo chmod 0755 /usr/local/bin/docker-credential-gcr",
    ]
  }

  provisioner "shell" {
    inline_shebang = "/bin/bash"
    inline = [
      "set -euo pipefail",
      "artifact_dir=/tmp/monad-root-artifacts",
      "mkdir -m 0700 -p $artifact_dir",
      "curl -fsSL --retry 5 --retry-delay 5 -o $artifact_dir/gcsfuse.deb '${local.root_artifact_lock.gcsfuse.url}'",
      "echo '${local.root_artifact_lock.gcsfuse.sha256}  '$artifact_dir/gcsfuse.deb | sha256sum --check --strict",
      "sudo apt-get update",
      "sudo apt-get install -y unzip jq net-tools qemu-utils make build-essential openssh-client openssh-server $artifact_dir/gcsfuse.deb", # TODO: openssh-server is updated to prevent security vulnerabilities
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
      "sudo systemctl start docker",
      "sudo usermod -aG docker $USER",
    ]
  }

  provisioner "shell" {
    inline_shebang = "/bin/bash"
    inline = [
      "set -euo pipefail",
      "archive=/tmp/bash-commons.tar.gz",
      "source_dir=/tmp/bash-commons",
      "curl -fsSL '${local.root_artifact_lock.bash_commons.url}' -o $archive",
      "echo '${local.root_artifact_lock.bash_commons.sha256}  '$archive | sha256sum --check --strict",
      "mkdir -p $source_dir",
      "tar -xzf $archive -C $source_dir --strip-components=1",
      "sudo mkdir -p /opt/gruntwork",
      "sudo cp -r $source_dir/modules/bash-commons/src /opt/gruntwork/bash-commons",
    ]
  }

  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-consul.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.consul_version} --sha256 ${local.root_artifact_lock.consul.sha256}"
  }

  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-nomad.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.nomad_version} --sha256 ${local.root_artifact_lock.nomad.sha256}"
  }

  # Install the ClickHouse client at the same version as the server so it's
  # available on every node without being downloaded at boot time.
  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-clickhouse-client.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.clickhouse_client_version} --sha512 ${local.root_artifact_lock.clickhouse_client.sha512}"
  }

  # Install CNI plugins (needed by Nomad bridge-mode networking on the
  # ClickHouse nodepool). Harmless on nodes that don't use them.
  provisioner "shell" {
    script          = "${local.shared_setup_dir}/install-cni-plugins.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.cni_plugin_version} --sha256 ${local.root_artifact_lock.cni_plugins.sha256}"
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
    inline_shebang = "/bin/bash"
    inline = [
      "set -euo pipefail",
      "artifact=/tmp/monad-root-artifacts/google-cloud-ops-agent.deb",
      "curl -fsSL --retry 5 --retry-delay 5 -o $artifact '${local.root_artifact_lock.google_cloud_ops_agent.url}'",
      "echo '${local.root_artifact_lock.google_cloud_ops_agent.sha256}  '$artifact | sha256sum --check --strict",
      "sudo apt-get install -y $artifact",
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

  post-processor "manifest" {
    output     = var.build_manifest_path
    strip_path = true
    custom_data = {
      environment            = var.image_environment
      image_family           = var.image_family
      image_name             = var.image_name
      source_image           = var.source_image
      source_project         = "ubuntu-os-cloud"
      source_revision        = var.source_revision
      root_input_lock_sha256 = local.root_artifact_lock_sha
    }
  }
}
