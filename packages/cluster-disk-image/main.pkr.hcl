packer {
  required_version = ">=1.8.4"
  required_plugins {
    amazon = {
      version = "1.3.2"
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
      name                = "ubuntu/images/*ubuntu-jammy-22.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    most_recent = true
    owners      = ["099720109477"]
  }

  ssh_username = "ubuntu"
  ami_description = "An AMI for e2b-orch"
}

build {
  name = "e2b-orch-disk-image"
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
      "sudo apt-get install -y unzip jq net-tools make gcc s3fs",
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
}
