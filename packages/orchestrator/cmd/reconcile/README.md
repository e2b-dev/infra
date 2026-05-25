Reconcile sweep
================

This small command scans Firecracker artifacts and the nat iptables table and writes a report.

Build:

```bash
cd packages/orchestrator
go build ./cmd/reconcile
```

Run (requires root to read iptables and /data0/tmp):

```bash
sudo ./reconcile -out /tmp/reconcile-report.txt
```

Usage & Safety
--------------

- Purpose: produce a read-only diagnostics report that lists Firecracker-related
	host artifacts (API sockets, uffd sockets, fc-metrics FIFOs) and captures the
	`nat` table via `iptables-save` for incident investigation.
- Read-only: the command does not modify iptables, network devices, or VMs.
- Privileges: reading the nat table usually requires root; run with `sudo` if
	necessary. The tool only executes `iptables-save` (no writes).
- Testability: the package exposes an injectable `ExecCommand` used by unit
	tests to avoid invoking system commands in test environments.
- Operational note: do not run this as an automated cleanup tool. Any
	remediation (deleting sockets, removing iptables rules, killing processes)
	must be performed under an explicit, reviewed runbook.

使用与安全说明（中文）
--------------------

- 目的：生成只读诊断报告，列出 Firecracker 相关的主机遗留工件，并保存 `nat` 表输出以便调查。
- 只读：该命令不会修改 iptables、网络设备或虚拟机。
- 权限：通常需要 root 权限读取 iptables；如需使用请用 `sudo`。该工具仅调用 `iptables-save`，不做写操作。
- 可测性：实现中支持注入 `ExecCommand`，测试时不会实际调用系统命令。
- 运行建议：此工具用于检测与调查，不要用于自动清理。任何清理或回收必须通过独立且受审查的操作手册执行。
