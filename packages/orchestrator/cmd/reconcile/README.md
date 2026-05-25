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


