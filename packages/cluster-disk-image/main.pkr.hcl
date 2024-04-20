packer {
  required_version = ">=1.8.4"
  required_plugins {
    amazon = {
      version = "1.0.16"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

source "amazon-ebs" "orch" {
  ami_name            = "e2b-orch-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  instance_type       = "t2.large"
  region              = var.aws_region
  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd/ubuntu-22.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    owners = ["099720109477"]  # Canonical
    most_recent = true
  }
  ssh_username = "ubuntu"
  ami_description = "An AMI for e2b-orch"
}

build {
  sources = ["source.amazon-ebs.orch"]

  provisioner "file" {
    source      = "${path.root}/setup/supervisord.conf"
    destination = "/tmp/supervisord.conf"
  }

  provisioner "file" {
    source      = "${path.root}/setup"
    destination = "/tmp"
  }

  provisioner "file" {
    source      = "${path.root}/setup/daemon.json"
    destination = "/tmp/daemon.json"
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

  provisioner "shell" {
    inline = [
      "sudo apt-get update -y",
      "sudo apt-get install -y unzip jq net-tools make gcc",
      "sudo curl -sSL https://github.com/s3fs-fuse/s3fs-fuse/archive/v1.89.tar.gz -o s3fs-fuse.tar.gz",
      "tar xzf s3fs-fuse.tar.gz",
      "cd s3fs-fuse-1.89",
      "sudo ./autogen.sh",
      "sudo ./configure",
      "sudo make",
      "sudo make install",
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
    script          = "${path.root}/setup/install-consul.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.consul_version}"
  }

  provisioner "shell" {
    script          = "${path.root}/setup/install-nomad.sh"
    execute_command = "chmod +x {{ .Path }}; {{ .Vars }} {{ .Path }} --version ${var.nomad_version}"
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
}
