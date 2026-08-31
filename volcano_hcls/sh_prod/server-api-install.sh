#!/bin/bash
set -euo pipefail

# =========================================================
# hk-prod server-api 节点一键安装脚本
# 包含：系统依赖、Docker、Go、NFS、Nomad、Consul、ClickHouse
# =========================================================

# HOME 有时未设置（sudo/su 无登录环境），后续 go env -w、.bashrc、.docker 都依赖它
export HOME="${HOME:-/root}"

# 运行日志：把全部 stdout/stderr 同时写到终端和带时间戳的日志文件
LOG_DIR="/var/log/server-api-install"
mkdir -p "$LOG_DIR"
LOG_FILE="${LOG_DIR}/install-$(date +%Y%m%d-%H%M%S).log"
# 每行前缀时间戳后再落盘，终端仍显示原始输出
exec > >(tee >(while IFS= read -r line; do printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$line"; done >> "$LOG_FILE")) 2>&1
echo "运行日志: $LOG_FILE"

GO_VERSION="1.26.5"
DATACENTER="hk-prod"
# HashiCorp 组件版本
NOMAD_VERSION="1.10.5"
CONSUL_VERSION="1.20.2"
VAULT_VERSION="1.21.0"
# 离线安装包目录（install-*.sh 从这里查找 zip）
INSTALLERS_DIR="/mnt/nfs/nomad_cluster_installers"
# LOCAL_IP="192.168.162.212"
# 获取本机内网 IP
LOCAL_IP=$(hostname -I | awk '{print $1}')

# 确保 HashiCorp 安装包存在；不存在则从官方源下载（香港可直连 releases.hashicorp.com）
ensure_installer() {
  local name="$1" version="$2"
  local dir="${INSTALLERS_DIR}/${name}"
  local zip="${dir}/${name}_${version}_linux_amd64.zip"
  if [ -s "$zip" ]; then
    echo "已存在安装包: $zip"
    return 0
  fi
  echo "下载 ${name} ${version} -> ${zip}"
  mkdir -p "$dir"
  curl -fSL "https://releases.hashicorp.com/${name}/${version}/${name}_${version}_linux_amd64.zip" -o "$zip"
}

echo "================================================="
echo "Step 1: Installing system dependencies"
echo "================================================="
apt-get update -y
apt-get install -y nfs-common nfs-kernel-server git curl supervisor
apt-get install -y postgresql-client redis-tools
apt-get install -y docker.io

# 配置 Docker 镜像加速（已验证可用的国内源）
cat > /etc/docker/daemon.json <<'EOF'
{
    "registry-mirrors": [
        "https://pee6w651.mirror.aliyuncs.com",
        "https://docker.m.daocloud.io",
        "https://docker.1ms.run"
    ],
    "dns": [
        "114.114.114.114",
        "223.5.5.5"
    ],
    "exec-opts": [
        "native.cgroupdriver=systemd"
    ],
    "max-concurrent-downloads": 10,
    "max-concurrent-uploads": 10
}
EOF
systemctl daemon-reload
systemctl restart docker

# 登录火山引擎容器镜像仓库（用于拉取 e2b 自定义镜像）
docker login --username 'crrobot@infrawaves' --password 'Fikypjfqobu2' mp-bp-cn-shanghai.cr.volces.com

echo "================================================="
echo "Step 2: Installing Go ${GO_VERSION}"
echo "================================================="
if /usr/local/go/bin/go version 2>/dev/null | grep -q "go${GO_VERSION}"; then
  echo "Go ${GO_VERSION} already installed, skipping"
else
  curl -sL "https://golang.google.cn/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm -f /tmp/go.tar.gz
fi
# 持久化 Go 环境变量到 /root/.bashrc（放前面覆盖系统自带 gccgo）
if ! grep -q '/usr/local/go/bin' /root/.bashrc 2>/dev/null; then
  cat >> /root/.bashrc <<'BASHEOF'
export PATH=/usr/local/go/bin:$PATH
export GOPROXY=https://goproxy.cn,direct
BASHEOF
fi
export PATH=/usr/local/go/bin:$PATH
export GOPROXY=https://goproxy.cn,direct
# 同时写入 go env 永久配置（写入 ~/.config/go/env）
/usr/local/go/bin/go env -w GOPROXY=https://goproxy.cn,direct
echo "Go: $(/usr/local/go/bin/go version)"

echo "================================================="
echo "Step 3: Setting up NFS server (export /mnt/nfs)"
echo "================================================="
# 本机作为 NFS server，导出 /mnt/nfs 供其它节点挂载；本机直接使用本地目录，无需自挂载
NFS_SHARE="/mnt/nfs"
# 允许同网段（LOCAL_IP 的 /24）的节点挂载
NFS_EXPORT_CIDR="$(echo "$LOCAL_IP" | awk -F. '{print $1"."$2"."$3".0/24"}')"
mkdir -p "$NFS_SHARE"
if ! grep -qE "^${NFS_SHARE}[[:space:]]" /etc/exports 2>/dev/null; then
  echo "${NFS_SHARE} ${NFS_EXPORT_CIDR}(rw,sync,no_subtree_check,no_root_squash)" >> /etc/exports
fi
systemctl enable --now nfs-server
exportfs -ra
echo "当前 NFS 导出:"; exportfs -v

cd /mnt/nfs
git clone https://github.com/agiping/e2b_infrawaves
mv e2b_infrawaves e2b_val
git clone https://github.com/orion-gmx/infra
INFRA_DIR="/mnt/nfs/infra"
HCL_DIR="${INFRA_DIR}/volcano_hcls/sh_prod"

echo "================================================="
echo "Step 4: Installing Nomad"
echo "================================================="
# 确保三个组件的离线包就位（本地/NFS 已有则复用，否则官方源下载）
ensure_installer nomad  "$NOMAD_VERSION"
ensure_installer consul "$CONSUL_VERSION"
ensure_installer vault  "$VAULT_VERSION"
cd /mnt/nfs/e2b_val/infrawaves/bashs
./install-nomad.sh --version "$NOMAD_VERSION"

echo "================================================="
echo "Step 5: Installing bash-commons"
echo "================================================="
# bash-commons 是 Gruntwork 官方库；NFS 上没有就从官方源 clone（香港可直连 GitHub）
if [ ! -d /mnt/nfs/bash-commons ]; then
  git clone https://github.com/gruntwork-io/bash-commons /mnt/nfs/bash-commons
fi
sudo mkdir -p /opt/gruntwork
cd /opt/gruntwork
cp -r /mnt/nfs/bash-commons ./
cd /opt/gruntwork/bash-commons/modules/bash-commons
./install.sh

echo "================================================="
echo "Step 6: Installing Consul and Vault"
echo "================================================="
cd /mnt/nfs/e2b_val/infrawaves/bashs
./install-consul.sh --version "$CONSUL_VERSION"
./install-vault.sh --version "$VAULT_VERSION"

echo "================================================="
echo "Step 7: Copying Nomad and Consul startup scripts"
echo "================================================="
sudo mkdir -p /opt/consul/bin
sudo mkdir -p /opt/nomad/bin
cp /mnt/nfs/e2b_val/infrawaves/bashs/run-consul.sh /opt/consul/bin
cp /mnt/nfs/e2b_val/infrawaves/bashs/run-nomad.sh /opt/nomad/bin

echo "================================================="
echo "Step 8: Starting Consul Server"
echo "================================================="
GOSSIP_KEY=$(consul keygen)
echo "GOSSIP_KEY: $GOSSIP_KEY"

mkdir -p /opt/consul/config /opt/consul/data
cat > /opt/consul/config/server.hcl <<EOF
datacenter = "${DATACENTER}"
data_dir = "/opt/consul/data"
server = true
bootstrap_expect = 1
bind_addr = "${LOCAL_IP}"
client_addr = "0.0.0.0"
node_name = "$(hostname)"
ui_config { enabled = true }
encrypt = "${GOSSIP_KEY}"
acl {
  enabled = true
  default_policy = "deny"
  enable_token_persistence = true
}
EOF

# systemd consul.service 需要 default.json 存在（ConditionFileNotEmpty），写一个空的占位文件
echo '{}' > /opt/consul/config/default.json

# 修复权限，consul 进程以 consul 用户运行
chown -R consul:consul /opt/consul/config /opt/consul/data

# 生成 consul.service（install-consul.sh 只装二进制不建 unit，fresh 节点上没有）
cat > /etc/systemd/system/consul.service <<EOF
[Unit]
Description=HashiCorp Consul
Requires=network-online.target
After=network-online.target

[Service]
User=consul
Group=consul
ExecStart=/opt/consul/bin/consul agent -config-dir /opt/consul/config -data-dir /opt/consul/data
ExecReload=/bin/kill -HUP \$MAINPID
KillMode=process
Restart=on-failure
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload

systemctl reset-failed consul 2>/dev/null || true
systemctl enable consul
systemctl start consul
sleep 8
export CONSUL_HTTP_TOKEN=$(consul acl bootstrap | grep SecretID | awk '{print $2}')
echo "CONSUL_TOKEN: $CONSUL_HTTP_TOKEN"

echo "================================================="
echo "Step 9: Starting Nomad Server + Client (combined)"
echo "================================================="
pkill -f 'nomad agent' 2>/dev/null || true
sleep 2

mkdir -p /opt/nomad/config /opt/nomad/data /opt/nomad/plugins
cat > /opt/nomad/config/server.hcl <<EOF
datacenter = "${DATACENTER}"
name       = "nomad-server-api"
region     = "global"
data_dir   = "/opt/nomad/data"

bind_addr = "0.0.0.0"
advertise {
  http = "${LOCAL_IP}"
  rpc  = "${LOCAL_IP}"
  serf = "${LOCAL_IP}"
}

leave_on_interrupt = true
leave_on_terminate = true

server {
  enabled          = true
  bootstrap_expect = 1
}

client {
  enabled   = true
  node_pool = "api"
  meta {
    "node_pool" = "api"
    "role"      = "api"
  }
  max_kill_timeout = "24h"
}

plugin "raw_exec" {
  config {
    enabled = true
  }
}

plugin_dir = "/opt/nomad/plugins"

plugin "docker" {
  config {
    volumes {
      enabled = true
    }
    auth {
      config = "/root/.docker/config.json"
    }
  }
}

log_level = "DEBUG"
log_json  = true

telemetry {
  collection_interval        = "5s"
  disable_hostname           = true
  prometheus_metrics         = true
  publish_allocation_metrics = true
  publish_node_metrics       = true
}

acl {
  enabled = true
}

limits {
  http_max_conns_per_client = 80
  rpc_max_conns_per_client  = 80
}

consul {
  address               = "127.0.0.1:8500"
  allow_unauthenticated = false
  token                 = "${CONSUL_HTTP_TOKEN}"
}
EOF

mkdir -p /opt/nomad/log

cat > /etc/supervisor/conf.d/run-nomad.conf <<'EOF'
[program:nomad]
command=/usr/local/bin/nomad agent -config /opt/nomad/config -data-dir /opt/nomad/data
stdout_logfile=/opt/nomad/log/nomad-stdout.log
stderr_logfile=/opt/nomad/log/nomad-error.log
numprocs=1
autostart=true
autorestart=true
stopsignal=INT
minfds=65536
user=root
EOF

supervisorctl reread
supervisorctl update
supervisorctl start nomad

# 等待 Nomad API 就绪并选出 leader（最多 60s），避免 acl bootstrap 卡死
echo "等待 Nomad leader 就绪..."
for i in $(seq 1 60); do
  if nomad operator api /v1/status/leader 2>/dev/null | grep -q ':'; then
    echo "Nomad leader 已就绪"
    break
  fi
  sleep 1
done

# ACL bootstrap 幂等：首次生成 token 并持久化到 /opt/nomad/nomad-acl.token；已存在则复用
NOMAD_TOKEN_FILE="/opt/nomad/nomad-acl.token"
if [ -s "$NOMAD_TOKEN_FILE" ]; then
  export NOMAD_TOKEN="$(cat "$NOMAD_TOKEN_FILE")"
  echo "复用已有 NOMAD_TOKEN"
else
  NOMAD_TOKEN="$(nomad acl bootstrap 2>/dev/null | grep 'Secret ID' | awk '{print $4}')"
  if [ -n "$NOMAD_TOKEN" ]; then
    echo "$NOMAD_TOKEN" > "$NOMAD_TOKEN_FILE"
    chmod 600 "$NOMAD_TOKEN_FILE"
    export NOMAD_TOKEN
  else
    echo "警告: acl bootstrap 未返回 token（可能集群已 bootstrap）。请手动提供已有 token 写入 $NOMAD_TOKEN_FILE 后重跑。"
  fi
fi
echo "NOMAD_TOKEN: ${NOMAD_TOKEN:-<空>}"
NOMAD_TOKEN=$NOMAD_TOKEN nomad node status || true

# 启用内存 oversubscription，使 memory_max 生效（集群级 raft 配置，立即生效无需重启）
nomad operator scheduler set-config -memory-oversubscription=true

echo "================================================="
echo "Step 10: Deploying ClickHouse"
echo "================================================="
# 预拉取镜像，避免 Nomad 调度时超时
# docker pull clickhouse/clickhouse-server:25.4.5.24
# nomad job run "${HCL_DIR}/clickhouse.hcl"

echo "================================================="
echo "Step 11: Running ClickHouse migrations"
echo "================================================="
export PATH=/usr/local/go/bin:$PATH
export GOPROXY=https://goproxy.cn,direct
# cd "${INFRA_DIR}/packages/clickhouse"
# GOOSE_DBSTRING="clickhouse://default:@${LOCAL_IP}:9000/${DATACENTER}_e2b_clickhouse" \
#   go tool goose -table "_migrations" -dir "migrations" clickhouse up

echo "================================================="
echo "Step 12: Preparing data directories"
echo "================================================="
# Loki 容器以非 root 用户运行，需要 777 权限
mkdir -p /mnt/data1/loki
chmod 777 /mnt/data1/loki

echo "================================================="
echo "Step 13: Deploying PG migrations (API + db-migrator)"
echo "================================================="
# 注意：如果 PG 是共享实例，可能已有 authenticated/trigger_user 角色，
# db-migrator 会自动跳过已执行的迁移。首次部署如果报 CREATE ROLE 错误，
# 需要手动处理：
#   1. 手动创建 auth.users 表
#   2. 手动执行 migration 20231220094836（跳过 CREATE USER trigger_user）
#   3. 用 goose 标记已执行，然后重新运行 migrator
# nomad job run "${HCL_DIR}/api.hcl"
# echo "等待 API 服务启动..."
# sleep 30

echo "================================================="
echo "Step 14: Deploying Loki"
echo "================================================="
# nomad job run "${HCL_DIR}/loki.hcl"

echo "================================================="
echo "Step 15: Deploying remaining services"
echo "================================================="
# otel-collector 和 log-collector 部署在所有节点
# nomad job run "${HCL_DIR}/otel-collector-prod.hcl"
# nomad job run "${HCL_DIR}/log-collector.hcl"
# client-proxy 部署在 API 节点
# nomad job run "${HCL_DIR}/client-proxy.hcl"

echo "================================================="
echo "Installation complete!"
echo "================================================="
echo
# echo "⚠  以下服务需要在 sandbox 节点加入后部署："
# echo "  nomad job run ${HCL_DIR}/orchestrator.hcl"
# echo "  nomad job run ${HCL_DIR}/template-manager.hcl"
echo
echo "请保存以下 Token："
echo "  CONSUL_TOKEN: ${CONSUL_HTTP_TOKEN}"
echo "  NOMAD_TOKEN:  ${NOMAD_TOKEN}"
echo "  GOSSIP_KEY:   ${GOSSIP_KEY}"

# 持久化 Token 到 /root/.bashrc
if ! grep -q 'NOMAD_TOKEN' /root/.bashrc 2>/dev/null; then
  cat >> /root/.bashrc <<TOKENEOF
export NOMAD_TOKEN="${NOMAD_TOKEN}"
export CONSUL_HTTP_TOKEN="${CONSUL_HTTP_TOKEN}"
TOKENEOF
fi

