#!/usr/bin/env bash
set -euo pipefail
exec &> >(tee -a /var/log/e2b-phase1.log)
echo "[$(date)] ═══ Phase 1: Installing prerequisites ═══"

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a
export NEEDRESTART_SUSPEND=1
export HOME=/root

# Disable needrestart interactive prompts
if [ -f /etc/needrestart/needrestart.conf ]; then
  sed -i "s/#\$nrconf{restart} = 'i';/\$nrconf{restart} = 'a';/" /etc/needrestart/needrestart.conf
fi
mkdir -p /etc/needrestart/conf.d
echo "\$nrconf{restart} = 'a';" > /etc/needrestart/conf.d/50-auto.conf

# Install system packages including HWE kernel 6.8+
echo "[$(date)] Installing system packages + HWE kernel..."
apt-get update -qq
apt-get install -y -qq \
  qemu-kvm libvirt-daemon-system libvirt-clients bridge-utils \
  virtinst cpu-checker git build-essential curl wget jq \
  python3-pip python3-venv net-tools ca-certificates \
  gnupg lsb-release snapd \
  linux-image-generic-hwe-22.04

# Ensure GRUB boots the newest (HWE) kernel
sed -i 's/^GRUB_DEFAULT=.*/GRUB_DEFAULT=0/' /etc/default/grub
update-grub

# Load kernel modules (current boot)
modprobe nbd max_part=16 nbds_max=128 || true
modprobe tun || true
modprobe kvm || true
modprobe kvm_intel nested=1 || true
modprobe kvm_amd nested=1 || true

# Apply hugepages
sysctl -p /etc/sysctl.d/90-e2b-hugepages.conf || true

# Install Docker CE from official repo (compose v2 plugin)
echo "[$(date)] Installing Docker CE..."
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
apt-get update -qq
apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
# Configure Docker: registry mirrors + lower concurrent downloads for reliability
mkdir -p /etc/docker
cat > /etc/docker/daemon.json <<'DOCKERJSON'
{
  "registry-mirrors": [
    "https://mirror.gcr.io"
  ],
  "max-concurrent-downloads": 3
}
DOCKERJSON
systemctl enable docker
systemctl start docker
usermod -aG docker e2b || true

# Install Go via snap
if ! command -v go &>/dev/null; then
  echo "[$(date)] Installing Go..."
  snap install go --classic
fi
export PATH=$PATH:/snap/bin:/usr/local/go/bin

# Install Node.js 20.x
if ! command -v node &>/dev/null; then
  echo "[$(date)] Installing Node.js 20.x..."
  curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
  apt-get install -y -qq nodejs
fi

# Clone the repository
echo "[$(date)] Cloning e2b-infra repository..."
if [[ ! -d /opt/e2b/infra ]]; then
  git clone https://github.com/e2b-dev/infra.git /opt/e2b/infra
fi
cd /opt/e2b/infra

# Checkout specific commit if requested
COMMIT="__COMMIT_HASH__"
if [[ -n "$COMMIT" ]]; then
  echo "[$(date)] Checking out commit: $COMMIT"
  git fetch --all
  git checkout "$COMMIT"
fi

echo "[$(date)] ═══ Phase 1 complete. HWE kernel installed. ═══"
echo "[$(date)] VM will reboot into kernel 6.8+ for Phase 2."
