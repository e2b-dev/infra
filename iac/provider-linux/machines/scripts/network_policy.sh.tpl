#!/usr/bin/env bash
set -euo pipefail

IFS=',' read -r -a EXTRA_PORTS <<< "$${OPEN_PORTS:-}"

# 定义核心服务端口
# SSH: 22
# Nomad: 4646 (HTTP), 4647 (RPC), 4648 (Serf TCP/UDP)
# Consul: 8300 (RPC), 8301 (LAN TCP/UDP), 8302 (WAN TCP/UDP), 8500 (HTTP), 8600 (DNS TCP/UDP)
# Custom: 6464 (User requested)
CORE_PORTS=(
  "22/tcp"
  "8300/tcp" "8301/tcp" "8301/udp" "8302/tcp" "8302/udp" "8500/tcp" "8600/tcp" "8600/udp"
  "4646/tcp" "4647/tcp" "4648/tcp" "4648/udp"
)

# 合并所有需要开放的端口
PORTS=("$${CORE_PORTS[@]}" "$${EXTRA_PORTS[@]}")

# -----------------------------------------------------------------------------
# 策略：优先检测正在运行的高级防火墙管理工具（Firewalld/UFW）。
# 如果发现它们处于活动状态，则直接使用它们并退出，避免冲突。
# 如果都没有，则回退到使用 iptables 直接管理。
# -----------------------------------------------------------------------------

# 1. 尝试 Firewalld (常见于 CentOS/RHEL/Fedora)
if systemctl is-active --quiet firewalld; then
  echo "Detected Firewalld is active. Using firewall-cmd to apply rules."
  for p in "$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    firewall-cmd --permanent --add-port="$p" || true
  done
  firewall-cmd --reload || true
  echo "Firewalld rules applied."
  exit 0
fi

# 2. 尝试 UFW (常见于 Ubuntu/Debian)
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  echo "Detected UFW is active. Using ufw to apply rules."
  for p in "$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    ufw allow "$p" || true
  done
  echo "UFW rules applied."
  exit 0
fi

# 3. 兜底方案：iptables (通用)
# 如果系统没有运行上述管理工具，我们假设可以直接操作 iptables
echo "No high-level firewall manager active. Falling back to iptables."

if ! command -v iptables >/dev/null 2>&1; then
  echo "Error: iptables not found."
  exit 1
fi

# 辅助函数：如果规则不存在则插入
add_rule() {
  local args=("$@")
  if ! iptables -C "$${args[@]}" 2>/dev/null; then
    iptables -I "$${args[@]}" || echo "Failed to add rule: $${args[*]}"
  fi
}

# 1. 允许本地回环
add_rule INPUT -i lo -j ACCEPT

# 2. 允许已建立的连接
add_rule INPUT -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

# 3. 允许 ICMP (Ping)
add_rule INPUT -p icmp -j ACCEPT

# 4. 允许指定端口
for p in "$${PORTS[@]}"; do
  [ -z "$p" ] && continue
  if [[ "$p" == *"/"* ]]; then
    port="$${p%%/*}"
    proto="$${p##*/}"
  else
    port="$p"
    proto="tcp"
  fi
  add_rule INPUT -p "$proto" --dport "$port" -j ACCEPT
done

# 尝试保存规则
if command -v netfilter-persistent >/dev/null 2>&1; then
  netfilter-persistent save || true
elif [ -d /etc/iptables ]; then
  iptables-save > /etc/iptables/rules.v4 || true
fi

echo "Iptables rules applied successfully."
