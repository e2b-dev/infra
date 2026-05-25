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
