packer {
  required_version = ">=1.8.4"

  required_plugins {
    amazon = {
      version = ">= 1.0.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

source "amazon-ebs" "ubuntu" {
  region  = var.aws_region
  profile = var.aws_profile

  instance_type = var.base_instance_type
  ami_name      = "${var.prefix}orch-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  ssh_username  = "ubuntu"

  // Ubuntu Server 22.04 LTS (HVM), SSD Volume Type
  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }

    owners      = ["099720109477"] # AWS Canonical
    most_recent = true
  }

  run_tags = {
    Name = "${var.prefix}orch-${formatdate("YYYY-MM-DD-hh-mm-ss", timestamp())}"
  }

  launch_block_device_mappings {
    device_name           = "/dev/sda1"
    volume_size           = 15
    volume_type           = "gp3"
    delete_on_termination = true
  }
}

locals {
  shared_setup_dir = "${path.root}/../../nomad-cluster-disk-image/setup"
}

build {
  sources = ["source.amazon-ebs.ubuntu"]

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

  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y nvme-cli unzip jq net-tools qemu-utils make build-essential openssh-client openssh-server", # TODO: openssh-server is updated to prevent security vulnerabilities
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

  provisioner "shell" {
    inline = [
      "sudo mkdir -p /opt/nomad/plugins",
    ]
  }

  // Install AWS CLI
  provisioner "shell" {
    inline = [
      "curl https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip -o awscliv2.zip",
      "unzip awscliv2.zip",
      "sudo ./aws/install"
    ]
  }

  // Install AWS ECR Docker Credential Helper
  // https://github.com/awslabs/amazon-ecr-credential-helper
  provisioner "shell" {
    inline = [
      "sudo apt-get install -y amazon-ecr-credential-helper",
    ]
  }

  // Install AWS S3 file system mounting tool
  // https://github.com/s3fs-fuse/s3fs-fuse
  provisioner "shell" {
    inline = [
      "sudo apt-get install -y s3fs",
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
}
