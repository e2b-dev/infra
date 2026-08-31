# ClickHouse 2026.22 到 2026.29 升级记录

本文记录 dev 环境将旧版 Docker ClickHouse 纳入 Nomad 管理，并补齐 2026.22 之后表结构迁移的过程。

## 结论

旧数据没有丢失。旧版 ClickHouse 使用 Docker named volume：

```text
clickhouse
/var/lib/docker/volumes/clickhouse/_data
```

Nomad 初始配置使用了宿主机目录 `/clickhouse/data`，因此启动的是另一份空数据。最终将 Nomad job 改为直接挂载旧 volume 的实际路径后，旧数据库和表恢复可见。

ClickHouse 迁移记录表是旧版 Goose 默认表 `goose_db_version`，不能使用新版默认的 `_migrations`，否则会被误认为所有迁移都未执行。

## 旧版部署方式

旧版通过 `packages/clickhouse/Makefile` 启动容器：

```bash
docker run -d --name clickhouse \
  -v clickhouse:/var/lib/clickhouse \
  clickhouse/clickhouse-server:25.4.5.24
```

因此数据实际存放在 Docker volume，而不是项目目录。

旧容器可以通过以下命令确认：

```bash
docker inspect clickhouse \
  --format '{{range .Mounts}}{{println .Type .Name .Source .Destination}}{{end}}'
```

应看到类似：

```text
volume clickhouse /var/lib/docker/volumes/clickhouse/_data /var/lib/clickhouse
```

## 不要直接新建空库

以下配置只在 ClickHouse 第一次使用空数据目录时帮助初始化：

```hcl
CLICKHOUSE_DB = "dev_e2b_clickhouse"
```

如果 `/var/lib/clickhouse` 已经有数据，容器启动不会根据这个变量补建数据库或表。不要删除旧 volume，也不要直接把旧数据复制到新目录后再猜测表结构。

## Nomad 数据卷配置

dev 的 [clickhouse.hcl](../volcano_hcls/sh_dev/clickhouse.hcl) 必须使用旧数据卷的实际宿主机路径：

```hcl
volumes = [
  "/var/lib/docker/volumes/clickhouse/_data:/var/lib/clickhouse",
  "local/config.xml:/etc/clickhouse-server/config.d/config.xml",
  "local/users.xml:/etc/clickhouse-server/users.d/users.xml",
]
```

仅写下面这种 named volume 名称时，Nomad Docker driver 可能将其解析为 allocation 下的临时目录，不能保证复用 Docker 已有的 named volume：

```hcl
"clickhouse:/var/lib/clickhouse"
```

由于这是本地磁盘数据，job 还应固定在拥有该 volume 的节点：

```hcl
constraint {
  attribute = "${node.unique.name}"
  value     = "nomad-server-api"
}
```

## 一次性接管步骤

### 1. 确认旧容器和 volume

```bash
docker ps -a | grep clickhouse
docker volume inspect clickhouse
du -sh /var/lib/docker/volumes/clickhouse/_data
```

不要执行 `docker volume rm clickhouse`。

### 2. 备份旧数据

在切换前建议备份：

```bash
tar -czf /data1/clickhouse-volume-backup-$(date +%F-%H%M%S).tar.gz \
  -C /var/lib/docker/volumes/clickhouse/_data .
```

### 3. 停止旧 Nomad job

```bash
nomad job stop -purge clickhouse
```

这不会删除 Docker volume，但会删除 Nomad allocation。

### 4. 部署修改后的 job

```bash
cd /mnt/nfs/dev/2026.29/infra
nomad job run volcano_hcls/sh_dev/clickhouse.hcl
```

### 5. 验证真实挂载

```bash
docker ps | grep clickhouse

docker inspect <新容器ID> \
  --format '{{range .Mounts}}{{println .Source "->" .Destination}}{{end}}'
```

必须看到：

```text
/var/lib/docker/volumes/clickhouse/_data -> /var/lib/clickhouse
```

### 6. 验证原有数据库和表

```bash
docker exec -it <新容器ID> clickhouse-client \
  --query "SHOW DATABASES"

docker exec -it <新容器ID> clickhouse-client \
  --database dev_e2b_clickhouse \
  --query "SHOW TABLES"
```

## 补齐 2026.22 之后的迁移

先查看当前迁移记录：

```bash
docker exec <容器ID> clickhouse-client \
  --database dev_e2b_clickhouse \
  --query "SELECT * FROM goose_db_version ORDER BY version_id"
```

本次从 2026.22 升级到 2026.29 时，旧库已执行到：

```text
20260417120000
```

待执行迁移为：

```text
20260512185000_add_webhook_deliveries.sql
20260702120000_add_sandbox_events_ttl_days.sql
20260818120000_reduce_team_metrics_ttl.sql
```

使用仓库中的 Goose，并明确指定旧 tracking 表：

```bash
cd /mnt/nfs/dev/2026.29/infra/packages/clickhouse

GOROOT=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.5.linux-amd64 \
GOOSE_DBSTRING='clickhouse://default:@192.168.162.30:9000/dev_e2b_clickhouse' \
go tool goose \
  -table goose_db_version \
  -dir migrations \
  clickhouse up
```

如果 `.envrc` 被 direnv 拦截，可以暂时忽略；上面的命令已经显式设置了连接字符串。也可以先执行：

```bash
direnv allow
```

但必须确认 `.envrc` 内容可信后再允许。

迁移完成后验证：

```bash
docker exec <容器ID> clickhouse-client \
  --database dev_e2b_clickhouse \
  --query "SELECT * FROM goose_db_version ORDER BY version_id"

docker exec <容器ID> clickhouse-client \
  --database dev_e2b_clickhouse \
  --query "SHOW TABLES"
```

本次补迁移新增了：

```text
webhook_deliveries
webhook_deliveries_local
```

并更新了 sandbox events 与 team metrics 的 TTL。已执行的迁移会被 Goose 跳过，不会重复建表。

## 后续升级注意事项

1. 每次升级前先确认 ClickHouse 当前实际挂载路径和 `goose_db_version`。
2. 不要将 `-table goose_db_version` 改成 `_migrations`，除非已经确认数据库确实使用 `_migrations`。
3. 先执行迁移状态检查，再执行 `goose up`。
4. ClickHouse job 目前没有自动执行 `clickhouse-migrator` task，版本升级后的 ClickHouse migrations 需要手动执行，或后续单独纳入 Nomad 部署流程。
5. ClickHouse 依赖本地 Docker volume，不能调度到没有 `/var/lib/docker/volumes/clickhouse/_data` 的其他节点。
6. 不要删除旧容器和 volume，除非已经完成独立备份并确认数据不再需要。
7. Go 工具链必须使用 Go 1.26.5；如果出现 `go1.26.3` 与 `go1.26.5` 混用，先设置：

```bash
export GOROOT=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.5.linux-amd64
export GOTOOLCHAIN=local
export PATH="$GOROOT/bin:$PATH"
```
