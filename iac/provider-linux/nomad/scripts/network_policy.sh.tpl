#!/usr/bin/env bash
set -euo pipefail

IFS=',' read -r -a PORTS <<< "$$${OPEN_PORTS:-}"

apply_ufw() {
  if ! command -v ufw >/dev/null 2>&1; then return 1; fi
  yes | ufw enable || true
  for p in "$$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    ufw allow "$p" || true
  done
}

apply_firewalld() {
  if ! command -v firewall-cmd >/dev/null 2>&1; then return 1; fi
  systemctl enable --now firewalld || true
  for p in "$$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    firewall-cmd --permanent --add-port="$p" || true
  done
  firewall-cmd --reload || true
}

apply_iptables() {
  local ok=0
  for p in "$$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    port="$$${p%%/*}"; proto="$$${p##*/}"
    if command -v iptables >/dev/null 2>&1; then
      iptables -I INPUT -p "$proto" --dport "$port" -j ACCEPT || true
      iptables -I OUTPUT -p "$proto" --dport "$port" -j ACCEPT || true
      ok=1
    fi
    if command -v ip6tables >/dev/null 2>&1; then
      ip6tables -I INPUT -p "$proto" --dport "$port" -j ACCEPT || true
      ip6tables -I OUTPUT -p "$proto" --dport "$port" -j ACCEPT || true
      ok=1
    fi
  done
  [ "$ok" -eq 1 ]
}

apply_nft() {
  if ! command -v nft >/dev/null 2>&1; then return 1; fi
  nft list ruleset >/dev/null 2>&1 || nft add table inet filter || true
  nft list chain inet filter input >/dev/null 2>&1 || nft add chain inet filter input '{ type filter hook input priority 0; }' || true
  nft list chain inet filter output >/dev/null 2>&1 || nft add chain inet filter output '{ type filter hook output priority 0; }' || true
  for p in "$$${PORTS[@]}"; do
    [ -z "$p" ] && continue
    port="$$${p%%/*}"; proto="$$${p##*/}"
    if [ "$proto" = "tcp" ]; then
      nft add rule inet filter input tcp dport "$port" accept || true
      nft add rule inet filter output tcp dport "$port" accept || true
    elif [ "$proto" = "udp" ]; then
      nft add rule inet filter input udp dport "$port" accept || true
      nft add rule inet filter output udp dport "$port" accept || true
    else
      nft add rule inet filter input meta l4proto "$proto" ct state new accept || true
      nft add rule inet filter output meta l4proto "$proto" ct state new accept || true
    fi
  done
}

#apply_ufw || apply_iptables || apply_nft || echo "No firewall tool applied; ports may already be open"

echo "Applied network policy for ports: $$${PORTS[*]}"
