#!/bin/bash
# Redirect all output to a log file for debugging
exec >> /var/log/auto_start_infrawaves.sh.log 2>&1

echo "================================================="
echo "Step 0: Installing git, nfs-common, docker"
echo "================================================="
apt-get update -y
apt-get install nfs-common -y
apt-get install git -y
apt install docker.io -y

echo "================================================="
echo "Step 1: Mounting NFS from Nomad Server..."
echo "================================================="
# 1. mount Nomad Server 节点的 NFS
mkdir -p /mnt/nfs
# 重试挂载，最多等 60 秒（NFS 服务可能还没就绪）
for i in $(seq 1 12); do
  mount 192.168.162.30:/mnt/nfs /mnt/nfs && break
  echo "NFS mount failed, retrying in 5s... ($i/12)"
  sleep 5
done
# 验证挂载成功，否则退出
if ! mountpoint -q /mnt/nfs; then
  echo "FATAL: NFS mount failed after 60s, aborting."
  exit 1
fi
echo "NFS mounted successfully."

echo "================================================="
echo "Step 2: Copying Firecracker files..."
echo "================================================="
# 2. 复制沙箱文件 /fc-envd, /fc-versions, /fc-kernels 到本地
sudo mkdir -p /opt/orchestrator/
cp /mnt/nfs/e2b_val/infrawaves/bins/orchestrator /opt/orchestrator/

sudo mkdir -p /opt/template-manager
cp /mnt/nfs/e2b_val/infrawaves/bins/template-manager /opt/template-manager/template-manager

sudo mkdir -p /fc-envd
sudo mkdir -p /fc-versions
sudo mkdir -p /fc-kernels
sudo mkdir -p /fc-busybox
cp -r /mnt/nfs/fc-kernels/* /fc-kernels/
cp -r /mnt/nfs/fc-versions/* /fc-versions/
cp -r /mnt/nfs/fc-envd/* /fc-envd/
cp -r /mnt/nfs/fc-busybox/* /fc-busybox

echo "================================================="
echo "Step 3: Setup Workspace"
echo "================================================="
#创建挂载目录
sudo mkdir -p /data1 /data2
#格式化磁盘
sudo mkfs.ext4 /dev/nvme0n1
sudo mkfs.ext4 /dev/nvme1n1
#挂载磁盘
sudo mount /dev/nvme0n1 /data1
sudo mount /dev/nvme1n1 /data2

# 备份 fstab
sudo cp /etc/fstab /etc/fstab.bak

# 先获取 UUID
UUID1=$(sudo blkid -s UUID -o value /dev/nvme0n1)
UUID2=$(sudo blkid -s UUID -o value /dev/nvme1n1)

# 直接追加到 fstab
echo "UUID=${UUID1} /data1 ext4 defaults,noatime,nodiratime 0 2" | sudo tee -a /etc/fstab
echo "UUID=${UUID2} /data2 ext4 defaults,noatime,nodiratime 0 2" | sudo tee -a /etc/fstab

# 验证
cat /etc/fstab | tail -5

#创建orchestrator工作目录
sudo mkdir -p /data1/orchestrator/
sudo mkdir -p /data1/tmp
sudo mkdir -p /data1/orchestrator/build
sudo mkdir -p /data1/orchestrator/build-templates
sudo mkdir -p /data1/orchestrator/kernels
sudo mkdir -p /data1/orchestrator/sandbox
sudo mkdir -p /data1/orchestrator/template
sudo mkdir -p /data1/orchestrator/volumes

echo "================================================="
echo "Step 4: Mounting snapshot and Configuring HugePages..."
echo "================================================="
sudo mkdir -p /mnt/snapshot-cache
sudo chmod 777 /mnt/snapshot-cache
sudo mount -t tmpfs -o size=4G tmpfs /mnt/snapshot-cache

#这个值需要根据内存调整。一个大页是2MB，一般按照内存的85%分配吧
sudo sysctl -w vm.nr_hugepages=200000
echo "vm.nr_hugepages=200000" | sudo tee -a /etc/sysctl.conf
grep HugePages_Total /proc/meminfo

echo "================================================="
echo "Step 5: Loading modprobe nbd..."
echo "================================================="
# 加载 NBD 模块
sudo modprobe -r nbd
sudo modprobe nbd nbds_max=4096 max_part=0
echo "nbd" | sudo tee -a /etc/modules
echo "options nbd nbds_max=4096" | sudo tee /etc/modprobe.d/nbd.conf
cat /sys/module/nbd/parameters/nbds_max
# 禁用 inotify
cat <<EOF | sudo tee /etc/udev/rules.d/97-nbd-device.rules
ACTION=="add|change", KERNEL=="nbd*", OPTIONS:="nowatch"
EOF
sudo udevadm control --reload-rules && sudo udevadm trigger

echo "================================================="
echo "Step 6: Installing Nomad..."
echo "================================================="
# 4. 安装 Nomad 和 Consul
cd /mnt/nfs/e2b_val/infrawaves/bashs || exit
./install-nomad.sh --version 1.10.5

echo "================================================="
echo "Step 7: Installing bash-commons..."
echo "================================================="
# 5. 安装 bash-commons
sudo mkdir -p /opt/gruntwork
cd /opt/gruntwork || exit
cp -r /mnt/nfs/bash-commons ./
cd /opt/gruntwork/bash-commons/modules/bash-commons || exit
./install.sh

echo "================================================="
echo "Step 8: Installing Consul and Vault..."
echo "================================================="
# 6. 安装 Consul 和 Vault
cd /mnt/nfs/e2b_val/infrawaves/bashs || exit
./install-consul.sh --version 1.20.2
./install-vault.sh --version 1.21.0

echo "================================================="
echo "Step 9: Copying Nomad and Consul startup scripts..."
echo "================================================="
# 7. 复制 Nomad 和 Consul 启动脚本
sudo mkdir -p /opt/consul/bin
sudo mkdir -p /opt/nomad/bin
cp /mnt/nfs/e2b_val/infrawaves/bashs/run-consul.sh /opt/consul/bin
cp /mnt/nfs/e2b_val/infrawaves/bashs/run-nomad.sh /opt/nomad/bin

echo "================================================="
echo "Step 10: Starting Nomad and Consul..."
echo "================================================="
# 8. 启动 Nomad 和 Consul
bash /mnt/nfs/e2b_val/infrawaves/bashs/start-sandbox-node.sh
sleep 30

echo "================================================="
echo "Step 11: Copying Nomad and Consul configuration files..."
echo "================================================="
# 9. 复制 Nomad 和 Consul 配置文件
sudo mkdir -p /opt/consul/config/
sudo mkdir -p /opt/nomad/config/
cp /mnt/nfs/e2b_val/infrawaves/configs/consul/default.json /opt/consul/config/
cp /mnt/nfs/e2b_val/infrawaves/configs/nomad/default.hcl /opt/nomad/config/

echo "================================================="
echo "Step 12: Injecting local IP and Restarting Consul..."
echo "================================================="
# 10. 向 Consul 配置文件注入本机内网 IP, 并重启 Consul
bash  /mnt/nfs/e2b_val/infrawaves/bashs/update-consul.sh
sleep 2
systemctl restart consul

echo "================================================="
echo "Step 13: Injecting local IP and Restarting Nomad..."
echo "================================================="
# 11. 向 Nomad 配置文件注入本机内网 IP, 并重启 Nomad
bash /mnt/nfs/e2b_val/infrawaves/bashs/update-nomad.sh
sleep 2
supervisorctl restart nomad

#12. 修改docker的认证配置
sudo mkdir /root/.docker

# 注意，这里踩过坑，之前忘记搞这个，沙箱就无法通外网
iptables -t nat -A POSTROUTING -s 10.12.0.0/16 -o eth0 -j MASQUERADE

echo "================================================="
echo "Step 14: 配置 SSH 免密认证"
echo "================================================="
# 环境新加节点追加ssh免密认证，ansible及ssh登录方便
key='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFMYaJ0M10YdvNCJ3c9tbdbqS5bI9X3I/Gbxt0ZhuvM8 root@dev-server-api'
grep -qF "$key" /root/.ssh/authorized_keys 2>/dev/null || echo "$key" >> /root/.ssh/authorized_keys