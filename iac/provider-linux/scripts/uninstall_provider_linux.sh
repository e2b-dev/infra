#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${FORCE_UNINSTALL:-}" ]]; then
  CONFIRM="UNINSTALL"
else
  echo "将卸载 Consul、Nomad，并清理相关系统配置与目录"
  read -r -p "请输入 UNINSTALL 确认继续: " CONFIRM
fi
[[ "${CONFIRM:-}" == "UNINSTALL" ]] || { echo "已取消"; exit 0; }

if [[ "$(id -u)" -eq 0 ]]; then SUDO=""; else SUDO="sudo"; fi
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a

echo "停止并禁用服务..."
$SUDO systemctl stop consul || true
$SUDO systemctl stop nomad || true

$SUDO systemctl disable consul || true
$SUDO systemctl disable nomad || true

echo "移除 systemd 配置..."
$SUDO rm -f /etc/systemd/system/consul.service.d/override.conf || true
$SUDO rm -f /etc/systemd/system/nomad.service.d/override.conf || true
$SUDO rm -f /etc/systemd/system/docker.service.d/proxy.conf || true
$SUDO systemctl daemon-reload || true

echo "清理 Consul/Nomad 配置与数据..."
$SUDO rm -f /etc/consul.d/consul.json || true
$SUDO rm -f /etc/consul.d/consul.hcl || true
$SUDO rm -f /etc/nomad.d/nomad.json || true
$SUDO rm -rf /var/lib/consul || true
$SUDO rm -rf /var/lib/nomad || true

echo "恢复 DNS 相关设置..."
$SUDO rm -f /etc/systemd/resolved.conf.d/consul.conf || true
$SUDO rm -f /etc/systemd/resolved.conf.d/docker.conf || true
$SUDO systemctl restart systemd-resolved || true

echo "清理 NBD 设备与内核模块..."
# 尝试卸载所有已连接的 nbd 设备，避免 rmmod 失败
for dev in /sys/block/nbd*; do
  if [[ -e "$dev/pid" ]]; then
    idx="${dev##*/nbd}"
    $SUDO nbd-client -d "/dev/nbd${idx}" || true
  fi
done
$SUDO rm -f /etc/udev/rules.d/97-nbd-device.rules || true
$SUDO rm -f /etc/modules-load.d/nbd.conf || true
$SUDO rm -f /etc/modprobe.d/nbd.conf || true
$SUDO modprobe -r nbd || true
$SUDO udevadm control --reload-rules || true
$SUDO udevadm trigger || true

echo "清理 NFS 相关配置与包..."
# 客户端卸载可能的挂载点 - 使用 lazy unmount (-l) 防止 Server 不可用时卡死
if mountpoint -q /e2b-share; then
  $SUDO umount -l -f /e2b-share || true
fi
# 服务器侧停止并禁用服务（若存在）
$SUDO systemctl disable --now nfs-kernel-server || true
$SUDO systemctl disable --now rpcbind || true
# 移除导出条目并刷新
if [[ -f /etc/exports ]]; then
  $SUDO sed -i '\|^/e2b-share |d' /etc/exports || true
  $SUDO exportfs -ra || true
fi
# 清理 NFS 目录
$SUDO rm -rf /e2b-share || true
# 移除 NFS 相关包（服务器与客户端）
if command -v apt-get >/dev/null 2>&1; then
  $SUDO apt-get purge -y nfs-kernel-server nfs-common rpcbind || true
  $SUDO apt-get autoremove -y || true
fi

echo "移除 Docker 相关配置..."
if [[ -f /etc/docker/daemon.json ]]; then
  $SUDO rm -f /etc/docker/daemon.json || true
fi
$SUDO systemctl restart docker || true

echo "移除 HashiCorp 包..."
if command -v apt-get >/dev/null 2>&1; then
  $SUDO dpkg -s consul      >/dev/null 2>&1 && $SUDO apt-get purge -y consul      || true
  $SUDO dpkg -s nomad       >/dev/null 2>&1 && $SUDO apt-get purge -y nomad       || true
  $SUDO dpkg -s nbd-client  >/dev/null 2>&1 && $SUDO apt-get purge -y nbd-client  || true
  $SUDO apt-get autoremove -y || true
fi
if command -v snap >/dev/null 2>&1; then
  $SUDO snap list 2>/dev/null | awk 'NR>1{print $1}' | grep -qx nomad && $SUDO snap remove nomad || true
fi
$SUDO rm -f /usr/bin/nomad /usr/bin/consul || true
$SUDO rm -f /usr/local/bin/nomad /usr/local/bin/consul || true
echo "移除 HashiCorp 源..."
$SUDO rm -f /etc/apt/sources.list.d/hashicorp.list || true
$SUDO rm -f /usr/share/keyrings/hashicorp-archive-keyring.gpg || true
if command -v apt-get >/dev/null 2>&1; then
  $SUDO apt-get update -y || true
fi

echo "清理工作目录与工件..."
$SUDO rm -rf /orchestrator/sandbox || true
$SUDO rm -rf /orchestrator/template || true
$SUDO rm -rf /orchestrator/build || true
$SUDO rm -rf /fc-vm || true
$SUDO rm -rf /fc-kernels || true
$SUDO rm -rf /fc-versions || true

echo "卸载完成"
