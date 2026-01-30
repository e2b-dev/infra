#!/usr/bin/env bash
set -euo pipefail

IFS=',' read -r -a EXTRA_PORTS <<< "${OPEN_PORTS}"

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
# 策略：优先使用 UFW (Ubuntu/Debian)，回退到 Firewalld (RHEL/CentOS)，
# 最后使用 iptables (通用)。
# UFW 会在配置时自动启用并持久化规则，无需额外保存操作。
# -----------------------------------------------------------------------------

# 1. 尝试 UFW (常见于 Ubuntu/Debian) - 优先方案
if command -v ufw >/dev/null 2>&1; then
  echo "Using UFW to apply firewall rules."
  
  # 清除所有 UFW 规则
  echo "Resetting UFW to clean state..."
  ufw --force reset
  
  # 清除所有 iptables/ip6tables 规则（UFW reset 后可能残留的手动规则）
  echo "Flushing any remaining iptables rules..."
  if command -v iptables >/dev/null 2>&1; then
    iptables -F 2>/dev/null || true
    iptables -X 2>/dev/null || true
    iptables -t nat -F 2>/dev/null || true
    iptables -t nat -X 2>/dev/null || true
    iptables -t mangle -F 2>/dev/null || true
    iptables -t mangle -X 2>/dev/null || true
  fi
  
  if command -v ip6tables >/dev/null 2>&1; then
    ip6tables -F 2>/dev/null || true
    ip6tables -X 2>/dev/null || true
    ip6tables -t nat -F 2>/dev/null || true
    ip6tables -t nat -X 2>/dev/null || true
    ip6tables -t mangle -F 2>/dev/null || true
    ip6tables -t mangle -X 2>/dev/null || true
  fi
  
  # 设置 UFW 默认策略（先拒绝入站，允许出站）
  ufw default deny incoming
  ufw default allow outgoing
  
  # 应用端口规则
  for p in "$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    echo "Allowing port $p"
    ufw allow "$p"
  done
  
  # 启用 UFW（应用所有规则）
  echo "Enabling UFW..."
  ufw --force enable
  
  echo "UFW rules applied and will persist across reboots."
  exit 0
fi

# 2. 尝试 Firewalld (常见于 CentOS/RHEL/Fedora)
if systemctl is-active --quiet firewalld; then
  echo "Detected Firewalld is active. Using firewall-cmd to apply rules."
  for p in "$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    firewall-cmd --permanent --add-port="$p"
  done
  firewall-cmd --reload
  echo "Firewalld rules applied."
  exit 0
fi

# 3. 兜底方案：iptables (通用)
echo "Warning: Neither UFW nor Firewalld available. Falling back to iptables."

if ! command -v iptables >/dev/null 2>&1; then
  echo "Error: iptables not found."
  exit 1
fi

# 辅助函数：如果规则不存在则插入
add_rule() {
  local args=("$@")
  if ! iptables -C "$${args[@]}" 2>/dev/null; then
    iptables -I "$${args[@]}"
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
  netfilter-persistent save
elif [ -d /etc/iptables ] || mkdir -p /etc/iptables; then
  iptables-save > /etc/iptables/rules.v4
fi

echo "Iptables rules applied (persistence not guaranteed)."
